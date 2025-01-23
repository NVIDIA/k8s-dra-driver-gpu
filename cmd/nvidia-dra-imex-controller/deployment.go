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

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/informers"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver/api/nvidia.com/resource/v1beta1"
)

const (
	DeploymentTemplatePath = "/templates/imex-daemon.tmpl.yaml"
)

type DeploymentTemplateData struct {
	Namespace                 string
	GenerateName              string
	Finalizer                 string
	ComputeDomainLabelKey     string
	ComputeDomainLabelValue   types.UID
	Replicas                  int
	ResourceClaimTemplateName string
}

type DeploymentManager struct {
	sync.Mutex

	config           *ManagerConfig
	waitGroup        sync.WaitGroup
	cancelContext    context.CancelFunc
	getComputeDomain GetComputeDomainFunc

	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer
	lister   appsv1listers.DeploymentLister

	resourceClaimTemplateManager *ResourceClaimTemplateManager
	imexChannelManager           *ImexChannelManager
	podManagers                  map[string]*DeploymentPodManager
}

func NewDeploymentManager(config *ManagerConfig, getComputeDomain GetComputeDomainFunc) *DeploymentManager {
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      computeDomainLabelKey,
				Operator: metav1.LabelSelectorOpExists,
			},
		},
	}

	factory := informers.NewSharedInformerFactoryWithOptions(
		config.clientsets.Core,
		informerResyncPeriod,
		informers.WithNamespace(config.driverNamespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = metav1.FormatLabelSelector(labelSelector)
		}),
	)

	informer := factory.Apps().V1().Deployments().Informer()
	lister := factory.Apps().V1().Deployments().Lister()

	m := &DeploymentManager{
		config:           config,
		getComputeDomain: getComputeDomain,
		factory:          factory,
		informer:         informer,
		lister:           lister,
		podManagers:      make(map[string]*DeploymentPodManager),
	}
	m.imexChannelManager = NewImexChannelManager(config)
	m.resourceClaimTemplateManager = NewResourceClaimTemplateManager(config, getComputeDomain)

	return m
}

func (m *DeploymentManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping Deployment manager: %v", err)
			}
		}
	}()

	if err := addComputeDomainLabelIndexer[*appsv1.Deployment](m.informer); err != nil {
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
		return fmt.Errorf("error adding event handlers for Deployment informer: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.factory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for Deployment failed")
	}

	if err := m.resourceClaimTemplateManager.Start(ctx); err != nil {
		return fmt.Errorf("error starting ResourceClaimTemplate manager: %w", err)
	}

	if err := m.imexChannelManager.Start(ctx); err != nil {
		return fmt.Errorf("error starting IMEX channel manager: %w", err)
	}

	return nil
}

func (m *DeploymentManager) Stop() error {
	if err := m.removeAllPodManagers(); err != nil {
		return fmt.Errorf("error removing all Pod managers: %w", err)
	}
	if err := m.imexChannelManager.Stop(); err != nil {
		return fmt.Errorf("error stopping IMEX channel manager: %w", err)
	}
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

func (m *DeploymentManager) Create(ctx context.Context, namespace string, replicas int, cd *nvapi.ComputeDomain) (*appsv1.Deployment, error) {
	d, err := getByComputeDomainUID[*appsv1.Deployment](ctx, m.informer, string(cd.UID))
	if err != nil {
		return nil, fmt.Errorf("error retrieving Deployment: %w", err)
	}
	if d != nil {
		return d, nil
	}

	rct, err := m.resourceClaimTemplateManager.Create(ctx, namespace, cd)
	if err != nil {
		return nil, fmt.Errorf("error creating ResourceClaimTemplate: %w", err)
	}

	templateData := DeploymentTemplateData{
		Namespace:                 m.config.driverNamespace,
		GenerateName:              fmt.Sprintf("%s-", cd.Name),
		Finalizer:                 computeDomainFinalizer,
		ComputeDomainLabelKey:     computeDomainLabelKey,
		ComputeDomainLabelValue:   cd.UID,
		Replicas:                  replicas,
		ResourceClaimTemplateName: rct.Name,
	}

	tmpl, err := template.ParseFiles(DeploymentTemplatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template file: %w", err)
	}

	var deploymentYaml bytes.Buffer
	if err := tmpl.Execute(&deploymentYaml, templateData); err != nil {
		return nil, fmt.Errorf("failed to execute template: %w", err)
	}

	var unstructuredObj unstructured.Unstructured
	err = yaml.Unmarshal(deploymentYaml.Bytes(), &unstructuredObj)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal yaml: %w", err)
	}

	var deployment appsv1.Deployment
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.UnstructuredContent(), &deployment)
	if err != nil {
		return nil, fmt.Errorf("failed to convert unstructured data to typed object: %w", err)
	}

	d, err = m.config.clientsets.Core.AppsV1().Deployments(deployment.Namespace).Create(ctx, &deployment, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("error creating Deployment: %w", err)
	}

	return d, nil
}

func (m *DeploymentManager) Delete(ctx context.Context, cdUID string) error {
	d, err := getByComputeDomainUID[*appsv1.Deployment](ctx, m.informer, cdUID)
	if err != nil {
		return fmt.Errorf("error retrieving Deployment: %w", err)
	}
	if d == nil {
		return nil
	}

	if err := m.RemoveFinalizer(ctx, d); err != nil {
		return fmt.Errorf("error removing finalizer on Deployment: %w", err)
	}

	err = m.config.clientsets.Core.AppsV1().Deployments(d.Namespace).Delete(ctx, d.Name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("erroring deleting Deployment: %w", err)
	}

	if err := m.resourceClaimTemplateManager.Delete(ctx, cdUID); err != nil {
		return fmt.Errorf("error deleting ResourceClaimTemplate: %w", err)
	}

	key := d.Spec.Selector.MatchLabels[computeDomainLabelKey]
	if err := m.removePodManager(key); err != nil {
		return fmt.Errorf("error removing Pod manager: %w", err)
	}

	return nil
}

func (m *DeploymentManager) RemoveFinalizer(ctx context.Context, d *appsv1.Deployment) error {
	newD := d.DeepCopy()

	newD.Finalizers = []string{}
	for _, f := range d.Finalizers {
		if f != computeDomainFinalizer {
			newD.Finalizers = append(newD.Finalizers, f)
		}
	}

	if _, err := m.config.clientsets.Core.AppsV1().Deployments(d.Namespace).Update(ctx, newD, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating Deployment: %w", err)
	}

	return nil
}

func (m *DeploymentManager) onAddOrUpdate(ctx context.Context, obj any) error {
	d, ok := obj.(*appsv1.Deployment)
	if !ok {
		return fmt.Errorf("failed to cast to Deployment")
	}

	d, err := m.lister.Deployments(d.Namespace).Get(d.Name)
	if err != nil && errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("erroring retreiving Deployment: %w", err)
	}

	klog.Infof("Processing added or updated Deployment: %s/%s", d.Namespace, d.Name)

	cd, err := m.getComputeDomain(d.Labels[computeDomainLabelKey])
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		if err := m.Delete(ctx, d.Labels[computeDomainLabelKey]); err != nil {
			return fmt.Errorf("error deleting Deployment '%s/%s': %w", d.Namespace, d.Name, err)
		}
		return nil
	}

	if err := m.addPodManager(ctx, d.Spec.Selector, int(*d.Spec.Replicas)); err != nil {
		return fmt.Errorf("error adding Pod manager '%s/%s': %w", d.Namespace, d.Name, err)
	}

	return nil
}

func (m *DeploymentManager) addPodManager(ctx context.Context, labelSelector *metav1.LabelSelector, numPods int) error {
	key := labelSelector.MatchLabels[computeDomainLabelKey]

	if _, exists := m.podManagers[key]; exists {
		return nil
	}

	podManager := NewDeploymentPodManager(m.config, m.imexChannelManager, labelSelector, numPods, m.getComputeDomain)

	if err := podManager.Start(ctx); err != nil {
		return fmt.Errorf("error creating Pod manager: %w", err)
	}

	m.Lock()
	m.podManagers[key] = podManager
	m.Unlock()

	return nil
}

func (m *DeploymentManager) removePodManager(key string) error {
	if _, exists := m.podManagers[key]; !exists {
		return nil
	}

	m.Lock()
	podManager := m.podManagers[key]
	m.Unlock()

	if err := podManager.Stop(); err != nil {
		return fmt.Errorf("error stopping Pod manager: %w", err)
	}

	m.Lock()
	delete(m.podManagers, key)
	m.Unlock()

	return nil
}

func (m *DeploymentManager) removeAllPodManagers() error {
	m.Lock()
	for key, pm := range m.podManagers {
		m.Unlock()
		if err := pm.Stop(); err != nil {
			return fmt.Errorf("error stopping Pod manager: %w", err)
		}
		m.Lock()
		delete(m.podManagers, key)
	}
	m.Unlock()
	return nil
}
