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
	DaemonSetResourceClaimTemplateTemplatePath = "/templates/compute-domain-daemon-claim-template.tmpl.yaml"
	WorkloadResourceClaimTemplateTemplatePath  = "/templates/compute-domain-workload-claim-template.tmpl.yaml"
)

type ResourceClaimTemplateTemplateData struct {
	Namespace               string
	Name                    string
	GenerateName            string
	Finalizer               string
	ComputeDomainLabelKey   string
	ComputeDomainLabelValue types.UID
	TargetLabelKey          string
	TargetLabelValue        string
	DeviceClassName         string
	DriverName              string
	ChannelConfig           *nvapi.ComputeDomainChannelConfig
	DaemonConfig            *nvapi.ComputeDomainDaemonConfig
}

type BaseResourceClaimTemplateManager struct {
	config           *ManagerConfig
	waitGroup        sync.WaitGroup
	cancelContext    context.CancelFunc
	getComputeDomain GetComputeDomainFunc

	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer

	cleanupManager *CleanupManager[*resourceapi.ResourceClaimTemplate]
}

type DaemonSetResourceClaimTemplateManager struct {
	*BaseResourceClaimTemplateManager
}

type WorkloadResourceClaimTemplateManager struct {
	*BaseResourceClaimTemplateManager
}

func newBaseResourceClaimTemplateManager(config *ManagerConfig, getComputeDomain GetComputeDomainFunc, labelSelector *metav1.LabelSelector, namespace string) *BaseResourceClaimTemplateManager {
	factory := informers.NewSharedInformerFactoryWithOptions(
		config.clientsets.Core,
		informerResyncPeriod,
		informers.WithNamespace(namespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = metav1.FormatLabelSelector(labelSelector)
		}),
	)

	informer := factory.Resource().V1beta1().ResourceClaimTemplates().Informer()

	m := &BaseResourceClaimTemplateManager{
		config:           config,
		getComputeDomain: getComputeDomain,
		factory:          factory,
		informer:         informer,
	}
	m.cleanupManager = NewCleanupManager[*resourceapi.ResourceClaimTemplate](informer, getComputeDomain, m.cleanup)

	return m
}

func (m *BaseResourceClaimTemplateManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping ResourceClaimTemplate  manager: %v", err)
			}
		}
	}()

	if err := addComputeDomainLabelIndexer[*resourceapi.ResourceClaimTemplate](m.informer); err != nil {
		return fmt.Errorf("error adding indexer for ComputeDomain label: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.factory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for ResourceClaimTemplate failed")
	}

	if err := m.cleanupManager.Start(ctx); err != nil {
		return fmt.Errorf("error starting cleanup manager: %w", err)
	}

	return nil
}

func (m *BaseResourceClaimTemplateManager) Stop() error {
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

func (m *BaseResourceClaimTemplateManager) Create(ctx context.Context, templatePath string, templateData *ResourceClaimTemplateTemplateData) (*resourceapi.ResourceClaimTemplate, error) {
	tmpl, err := template.ParseFiles(templatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template file: %w", err)
	}

	var resourceClaimTemplateYaml bytes.Buffer
	if err := tmpl.Execute(&resourceClaimTemplateYaml, templateData); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}

	var unstructuredObj unstructured.Unstructured
	err = yaml.Unmarshal(resourceClaimTemplateYaml.Bytes(), &unstructuredObj)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal yaml: %w", err)
	}

	var resourceClaimTemplate resourceapi.ResourceClaimTemplate
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.UnstructuredContent(), &resourceClaimTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured data to typed object: %w", err)
	}

	rct, err := m.config.clientsets.Core.ResourceV1beta1().ResourceClaimTemplates(resourceClaimTemplate.Namespace).Create(ctx, &resourceClaimTemplate, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("error creating ResourceClaimTemplate: %w", err)
	}

	return rct, nil
}

func (m *BaseResourceClaimTemplateManager) Delete(ctx context.Context, cdUID string) error {
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

	if rct.GetDeletionTimestamp() != nil {
		return nil
	}

	err = m.config.clientsets.Core.ResourceV1beta1().ResourceClaimTemplates(rct.Namespace).Delete(ctx, rct.Name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("erroring deleting ResourceClaimTemplate: %w", err)
	}

	return nil
}

func (m *BaseResourceClaimTemplateManager) RemoveFinalizer(ctx context.Context, cdUID string) error {
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

func (m *BaseResourceClaimTemplateManager) AssertRemoved(ctx context.Context, cdUID string) error {
	rcts, err := getByComputeDomainUID[*resourceapi.ResourceClaimTemplate](ctx, m.informer, cdUID)
	if err != nil {
		return fmt.Errorf("error retrieving ResourceClaimTemplate: %w", err)
	}
	if len(rcts) != 0 {
		return fmt.Errorf("still exists")
	}
	return nil
}

func (m *BaseResourceClaimTemplateManager) cleanup(ctx context.Context, cdUID string) error {
	if err := m.Delete(ctx, cdUID); err != nil {
		return fmt.Errorf("error deleting ResourceClaimTemplate: %w", err)
	}
	if err := m.RemoveFinalizer(ctx, cdUID); err != nil {
		return fmt.Errorf("error removing ResourceClaimTemplate finalizer: %w", err)
	}
	return nil
}

func NewDaemonSetResourceClaimTemplateManager(config *ManagerConfig, getComputeDomain GetComputeDomainFunc) *DaemonSetResourceClaimTemplateManager {
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      computeDomainLabelKey,
				Operator: metav1.LabelSelectorOpExists,
			},
			{
				Key:      computeDomainResourceClaimTemplateTargetLabelKey,
				Operator: metav1.LabelSelectorOpIn,
				Values:   []string{computeDomainResourceClaimTemplateTargetDaemon},
			},
		},
	}

	base := newBaseResourceClaimTemplateManager(config, getComputeDomain, labelSelector, config.driverNamespace)

	m := &DaemonSetResourceClaimTemplateManager{
		BaseResourceClaimTemplateManager: base,
	}

	return m
}

func (m *DaemonSetResourceClaimTemplateManager) Create(ctx context.Context, namespace string, cd *nvapi.ComputeDomain) (*resourceapi.ResourceClaimTemplate, error) {
	rcts, err := getByComputeDomainUID[*resourceapi.ResourceClaimTemplate](ctx, m.informer, string(cd.UID))
	if err != nil {
		return nil, fmt.Errorf("error retrieving ResourceClaimTemplate: %w", err)
	}
	if len(rcts) > 1 {
		return nil, fmt.Errorf("more than one ResourceClaimTemplate found with same ComputeDomain UID")
	}
	if len(rcts) == 1 {
		return rcts[0], nil
	}

	daemonConfig := nvapi.DefaultComputeDomainDaemonConfig()
	daemonConfig.DomainID = string(cd.UID)

	templateData := ResourceClaimTemplateTemplateData{
		Namespace:               namespace,
		GenerateName:            fmt.Sprintf("%s-daemon-claim-template-", cd.Name),
		Finalizer:               computeDomainFinalizer,
		ComputeDomainLabelKey:   computeDomainLabelKey,
		ComputeDomainLabelValue: cd.UID,
		TargetLabelKey:          computeDomainResourceClaimTemplateTargetLabelKey,
		TargetLabelValue:        computeDomainResourceClaimTemplateTargetDaemon,
		DeviceClassName:         computeDomainDaemonDeviceClass,
		DriverName:              DriverName,
		DaemonConfig:            daemonConfig,
	}

	rct, err := m.BaseResourceClaimTemplateManager.Create(ctx, DaemonSetResourceClaimTemplateTemplatePath, &templateData)
	if err != nil {
		return nil, fmt.Errorf("error creating ResourceClaimTemplate from base: %w", err)
	}

	return rct, nil
}

func NewWorkloadResourceClaimTemplateManager(config *ManagerConfig, getComputeDomain GetComputeDomainFunc) *WorkloadResourceClaimTemplateManager {
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

	base := newBaseResourceClaimTemplateManager(config, getComputeDomain, labelSelector, "")

	m := &WorkloadResourceClaimTemplateManager{
		BaseResourceClaimTemplateManager: base,
	}

	return m
}

func (m *WorkloadResourceClaimTemplateManager) Create(ctx context.Context, namespace, name string, cd *nvapi.ComputeDomain) (*resourceapi.ResourceClaimTemplate, error) {
	rcts, err := getByComputeDomainUID[*resourceapi.ResourceClaimTemplate](ctx, m.informer, string(cd.UID))
	if err != nil {
		return nil, fmt.Errorf("error retrieving ResourceClaimTemplate: %w", err)
	}
	if len(rcts) > 1 {
		return nil, fmt.Errorf("more than one ResourceClaimTemplate found with same ComputeDomain UID")
	}
	if len(rcts) == 1 {
		return rcts[0], nil
	}

	channelConfig := nvapi.DefaultComputeDomainChannelConfig()
	channelConfig.DomainID = string(cd.UID)

	templateData := ResourceClaimTemplateTemplateData{
		Namespace:               namespace,
		Name:                    name,
		Finalizer:               computeDomainFinalizer,
		ComputeDomainLabelKey:   computeDomainLabelKey,
		ComputeDomainLabelValue: cd.UID,
		TargetLabelKey:          computeDomainResourceClaimTemplateTargetLabelKey,
		TargetLabelValue:        computeDomainResourceClaimTemplateTargetWorkload,
		DeviceClassName:         computeDomainDefaultChannelDeviceClass,
		DriverName:              DriverName,
		ChannelConfig:           channelConfig,
	}

	rct, err := m.BaseResourceClaimTemplateManager.Create(ctx, WorkloadResourceClaimTemplateTemplatePath, &templateData)
	if err != nil {
		return nil, fmt.Errorf("error creating ResourceClaimTemplate from base: %w", err)
	}

	return rct, nil
}
