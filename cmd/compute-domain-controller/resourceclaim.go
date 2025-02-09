/*
 * Copyright (c) 2025 NVIDIA CORPORATION.  All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package main

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"text/template"

	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
)

const (
	WorkloadResourceClaimTemplateTemplatePath = "/templates/compute-domain-workload-claim-template.tmpl.yaml"
)

type WorkloadResourceClaimTemplateTemplateData struct {
	Namespace               string
	Name                    string
	Finalizer               string
	ComputeDomainLabelKey   string
	ComputeDomainLabelValue types.UID
	TargetLabelKey          string
	TargetLabelValue        string
	DeviceClassName         string
	ChannelID               int
	DriverName              string
	ChannelConfig           *nvapi.ComputeDomainChannelConfig
	WithDefaultChannel      bool
}

type WorkloadResourceClaimTemplateManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer
}

func NewWorkloadResourceClaimTemplateManager(config *ManagerConfig) *WorkloadResourceClaimTemplateManager {
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      computeDomainLabelKey,
				Operator: metav1.LabelSelectorOpExists,
			},
			{
				Key:      computeDomainResourceClaimTemplateTargetLabelKey,
				Operator: metav1.LabelSelectorOpIn,
				Values:   []string{computeDomainResourceClaimTemplateTargetWorkload},
			},
		},
	}

	factory := informers.NewSharedInformerFactoryWithOptions(
		config.clientsets.Core,
		informerResyncPeriod,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = metav1.FormatLabelSelector(labelSelector)
		}),
	)

	informer := factory.Resource().V1beta1().ResourceClaimTemplates().Informer()

	m := &WorkloadResourceClaimTemplateManager{
		config:   config,
		factory:  factory,
		informer: informer,
	}

	return m
}

func (m *WorkloadResourceClaimTemplateManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping ResourceClaimTemplate manager: %v", err)
			}
		}
	}()

	if err := addComputeDomainLabelIndexer[*resourceapi.ResourceClaimTemplate](m.informer); err != nil {
		return fmt.Errorf("error adding indexer for MulitNodeEnvironment label: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.factory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for ResourceClaimTemplate failed")
	}

	return nil
}

func (m *WorkloadResourceClaimTemplateManager) Stop() error {
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

func (m *WorkloadResourceClaimTemplateManager) Create(ctx context.Context, namespace, name string, channel int, cd *nvapi.ComputeDomain) (*resourceapi.ResourceClaimTemplate, error) {
	rct, err := m.config.clientsets.Core.ResourceV1beta1().ResourceClaimTemplates(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return nil, fmt.Errorf("error getting ResourceClaimTemplate: %w", err)
	}
	if err == nil && rct.Labels[computeDomainLabelKey] != string(cd.UID) {
		return nil, fmt.Errorf("existing ResourceClaimTemplate '%s/%s' not associated with ComputeDomain '%v'", namespace, name, cd.UID)
	}
	if err == nil {
		return rct, nil
	}

	channelConfig := nvapi.DefaultComputeDomainChannelConfig()
	channelConfig.WaitForReady = true
	channelConfig.DomainID = string(cd.UID)

	templateData := WorkloadResourceClaimTemplateTemplateData{
		Namespace:               namespace,
		Name:                    name,
		Finalizer:               computeDomainFinalizer,
		ComputeDomainLabelKey:   computeDomainLabelKey,
		ComputeDomainLabelValue: cd.UID,
		TargetLabelKey:          computeDomainResourceClaimTemplateTargetLabelKey,
		TargetLabelValue:        computeDomainResourceClaimTemplateTargetWorkload,
		DeviceClassName:         computeDomainChannelDeviceClass,
		ChannelID:               channel,
		DriverName:              DriverName,
		ChannelConfig:           channelConfig,
		WithDefaultChannel:      false,
	}

	if cd.Spec.Mode == nvapi.ComputeDomainModeDelayed {
		templateData.DeviceClassName = computeDomainDefaultChannelDeviceClass
		templateData.ChannelConfig = channelConfig
		templateData.WithDefaultChannel = true
	}

	tmpl, err := template.ParseFiles(WorkloadResourceClaimTemplateTemplatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template file: %w", err)
	}

	var resourceClaimTemplateYaml bytes.Buffer
	if err := tmpl.Execute(&resourceClaimTemplateYaml, templateData); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}

	var unstructuredObj unstructured.Unstructured
	if err := yaml.Unmarshal(resourceClaimTemplateYaml.Bytes(), &unstructuredObj); err != nil {
		return nil, fmt.Errorf("failed to unmarshal yaml: %w", err)
	}

	var resourceClaimTemplate resourceapi.ResourceClaimTemplate
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.UnstructuredContent(), &resourceClaimTemplate); err != nil {
		return nil, fmt.Errorf("failed to convert unstructured data to typed object: %w", err)
	}

	rct, err = m.config.clientsets.Core.ResourceV1beta1().ResourceClaimTemplates(namespace).Create(ctx, &resourceClaimTemplate, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("error creating ResourceClaimTemplate: %w", err)
	}

	return rct, nil
}

func (m *WorkloadResourceClaimTemplateManager) Delete(ctx context.Context, cdUID string) error {
	rcts, err := getByComputeDomainUID[*resourceapi.ResourceClaimTemplate](ctx, m.informer, cdUID)
	if err != nil {
		return fmt.Errorf("error retrieving ResourceClaimTemplates: %w", err)
	}
	if len(rcts) == 0 {
		return nil
	}

	for _, rct := range rcts {
		if rct.GetDeletionTimestamp() != nil {
			continue
		}

		err := m.config.clientsets.Core.ResourceV1beta1().ResourceClaimTemplates(rct.Namespace).Delete(ctx, rct.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("erroring deleting ResourceClaimTemplate: %w", err)
		}
	}

	return nil
}

func (m *WorkloadResourceClaimTemplateManager) RemoveFinalizer(ctx context.Context, cdUID string) error {
	rcts, err := getByComputeDomainUID[*resourceapi.ResourceClaimTemplate](ctx, m.informer, cdUID)
	if err != nil {
		return fmt.Errorf("error retrieving ResourceClaimTemplate: %w", err)
	}
	if len(rcts) > 1 {
		return fmt.Errorf("more than one ResourceClaimTemplate found with same ComputeDomain UID")
	}
	if len(rcts) == 0 {
		return nil
	}

	rct := rcts[0]

	if rct.GetDeletionTimestamp() == nil {
		return fmt.Errorf("attempting to remove finalizer before ResourceClaimTemplate marked for deletion")
	}

	newRCT := rct.DeepCopy()
	newRCT.Finalizers = []string{}
	for _, f := range rct.Finalizers {
		if f != computeDomainFinalizer {
			newRCT.Finalizers = append(newRCT.Finalizers, f)
		}
	}
	if len(rct.Finalizers) == len(newRCT.Finalizers) {
		return nil
	}

	if _, err = m.config.clientsets.Core.ResourceV1beta1().ResourceClaimTemplates(rct.Namespace).Update(ctx, newRCT, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating ResourceClaimTemplate: %w", err)
	}

	return nil
}
