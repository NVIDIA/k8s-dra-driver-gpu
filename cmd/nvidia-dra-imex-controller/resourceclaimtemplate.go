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
	resourcelisters "k8s.io/client-go/listers/resource/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver/api/nvidia.com/resource/gpu/v1alpha1"
)

const (
	ImexDaemonDeviceClass             = "imex-daemon.nvidia.com"
	ResourceClaimTemplateTemplatePath = "/templates/imex-daemon-claim-template.tmpl.yaml"
)

type ResourceClaimTemplateTemplateData struct {
	Namespace                      string
	GenerateName                   string
	Finalizer                      string
	MultiNodeEnvironmentLabelKey   string
	MultiNodeEnvironmentLabelValue types.UID
	DeviceClassName                string
	DriverName                     string
	ImexDaemonConfig               *nvapi.ImexDaemonConfig
}

type ResourceClaimTemplateManager struct {
	config                     *ManagerConfig
	waitGroup                  sync.WaitGroup
	cancelContext              context.CancelFunc
	multiNodeEnvironmentExists MultiNodeEnvironmentExistsFunc

	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer
	lister   resourcelisters.ResourceClaimTemplateLister
}

func NewResourceClaimTemplateManager(config *ManagerConfig, mneExists MultiNodeEnvironmentExistsFunc) *ResourceClaimTemplateManager {
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      multiNodeEnvironmentLabelKey,
				Operator: metav1.LabelSelectorOpExists,
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
	lister := factory.Resource().V1beta1().ResourceClaimTemplates().Lister()

	m := &ResourceClaimTemplateManager{
		config:                     config,
		multiNodeEnvironmentExists: mneExists,
		factory:                    factory,
		informer:                   informer,
		lister:                     lister,
	}

	return m
}

func (m *ResourceClaimTemplateManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping ResourceClaim manager: %v", err)
			}
		}
	}()

	if err := addMultiNodeEnvironmentLabelIndexer[*resourceapi.ResourceClaimTemplate](m.informer); err != nil {
		return fmt.Errorf("error adding indexer for MulitNodeEnvironment label: %w", err)
	}

	_, err := m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			m.config.workQueue.Enqueue(obj, m.onAddOrUpdate)
		},
		UpdateFunc: func(objOld, objNew any) {
			m.config.workQueue.Enqueue(objNew, m.onAddOrUpdate)
		},
	})
	if err != nil {
		return fmt.Errorf("error adding event handlers for ResourceClaimTemplate informer: %w", err)
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

func (m *ResourceClaimTemplateManager) Stop() error {
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

func (m *ResourceClaimTemplateManager) Create(ctx context.Context, namespace string, mne *nvapi.MultiNodeEnvironment) (*resourceapi.ResourceClaimTemplate, error) {
	rct, err := getByMultiNodeEnvironmentUID[*resourceapi.ResourceClaimTemplate](ctx, m.informer, string(mne.UID))
	if err != nil {
		return nil, fmt.Errorf("error retrieving ResourceClaimTemplate: %w", err)
	}
	if rct != nil {
		return rct, nil
	}

	imexDaemonConfig := nvapi.DefaultImexDaemonConfig()
	imexDaemonConfig.NumNodes = mne.Spec.NumNodes
	imexDaemonConfig.DomainID = string(mne.UID)

	templateData := ResourceClaimTemplateTemplateData{
		Namespace:                      m.config.driverNamespace,
		GenerateName:                   fmt.Sprintf("%s-claim-template-", mne.Name),
		Finalizer:                      multiNodeEnvironmentFinalizer,
		MultiNodeEnvironmentLabelKey:   multiNodeEnvironmentLabelKey,
		MultiNodeEnvironmentLabelValue: mne.UID,
		DeviceClassName:                ImexDaemonDeviceClass,
		DriverName:                     DriverName,
		ImexDaemonConfig:               imexDaemonConfig,
	}

	tmpl, err := template.ParseFiles(ResourceClaimTemplateTemplatePath)
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

	rct, err = m.config.clientsets.Core.ResourceV1beta1().ResourceClaimTemplates(namespace).Create(ctx, &resourceClaimTemplate, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("error creating ResourceClaimTemplate: %w", err)
	}

	return rct, nil
}

func (m *ResourceClaimTemplateManager) Delete(ctx context.Context, mneUID string) error {
	rct, err := getByMultiNodeEnvironmentUID[*resourceapi.ResourceClaimTemplate](ctx, m.informer, mneUID)
	if err != nil {
		return fmt.Errorf("error retrieving ResourceClaim: %w", err)
	}
	if rct == nil {
		return nil
	}

	if err := m.RemoveFinalizer(ctx, rct.Namespace, rct.Name); err != nil {
		return fmt.Errorf("error removing finalizer on ResourceClaimTemplate '%s/%s': %w", rct.Namespace, rct.Name, err)
	}

	err = m.config.clientsets.Core.ResourceV1beta1().ResourceClaimTemplates(rct.Namespace).Delete(ctx, rct.Name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("erroring deleting ResourceClaimTemplate: %w", err)
	}

	return nil
}

func (m *ResourceClaimTemplateManager) RemoveFinalizer(ctx context.Context, namespace, name string) error {
	rct, err := m.lister.ResourceClaimTemplates(namespace).Get(name)
	if err != nil && errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("error retrieving ResourceClaimTemplate: %w", err)
	}

	newRCT := rct.DeepCopy()

	newRCT.Finalizers = []string{}
	for _, f := range rct.Finalizers {
		if f != multiNodeEnvironmentFinalizer {
			newRCT.Finalizers = append(newRCT.Finalizers, f)
		}
	}

	_, err = m.config.clientsets.Core.ResourceV1beta1().ResourceClaimTemplates(namespace).Update(ctx, newRCT, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("error updating ResourceClaimTemplate: %w", err)
	}

	return nil
}

func (m *ResourceClaimTemplateManager) onAddOrUpdate(ctx context.Context, obj any) error {
	rct, ok := obj.(*resourceapi.ResourceClaimTemplate)
	if !ok {
		return fmt.Errorf("failed to cast to ResourceClaimTemplate")
	}

	rct, err := m.lister.ResourceClaimTemplates(rct.Namespace).Get(rct.Name)
	if err != nil && errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("error retreiving ResourceClaimTemplate: %w", err)
	}

	klog.Infof("Processing added or updated ResourceClaimTemplate: %s/%s", rct.Namespace, rct.Name)

	exists, err := m.multiNodeEnvironmentExists(rct.Labels[multiNodeEnvironmentLabelKey])
	if err != nil {
		return fmt.Errorf("error checking if owner exists: %w", err)
	}
	if !exists {
		if err := m.Delete(ctx, rct.Labels[multiNodeEnvironmentLabelKey]); err != nil {
			return fmt.Errorf("error deleting ResourceClaimTemplate '%s/%s': %w", rct.Namespace, rct.Name, err)
		}
		return nil
	}

	return nil
}
