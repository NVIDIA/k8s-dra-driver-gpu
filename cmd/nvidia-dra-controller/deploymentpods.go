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
	"context"
	"fmt"
	"slices"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

type DeploymentPodManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  informers.SharedInformerFactory
	informer cache.SharedInformer
	lister   corev1listers.PodLister

	nodeSelector              corev1.NodeSelector
	multiNodeEnvironmentLabel string
	numPods                   int

	imexChannelManager *ImexChannelManager
}

func NewDeploymentPodManager(config *ManagerConfig, imexChannelManager *ImexChannelManager, labelSelector *metav1.LabelSelector, numPods int) *DeploymentPodManager {
	factory := informers.NewSharedInformerFactoryWithOptions(
		config.clientsets.Core,
		informerResyncPeriod,
		informers.WithNamespace(config.driverNamespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = metav1.FormatLabelSelector(labelSelector)
		}),
	)

	informer := factory.Core().V1().Pods().Informer()
	lister := factory.Core().V1().Pods().Lister()

	nodeSelector := corev1.NodeSelector{
		NodeSelectorTerms: []corev1.NodeSelectorTerm{
			{
				MatchExpressions: []corev1.NodeSelectorRequirement{
					{
						Key:      "kubernetes.io/hostname",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{},
					},
				},
			},
		},
	}

	m := &DeploymentPodManager{
		config:                    config,
		factory:                   factory,
		informer:                  informer,
		lister:                    lister,
		nodeSelector:              nodeSelector,
		multiNodeEnvironmentLabel: labelSelector.MatchLabels[multiNodeEnvironmentLabelKey],
		numPods:                   numPods,
		imexChannelManager:        imexChannelManager,
	}

	return m
}

func (m *DeploymentPodManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping DeploymentPod manager: %v", err)
			}
		}
	}()

	_, err := m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			m.config.workQueue.Enqueue(obj, m.onPodAddOrUpdate)
		},
		UpdateFunc: func(objOld, objNew any) {
			m.config.workQueue.Enqueue(objNew, m.onPodAddOrUpdate)
		},
	})
	if err != nil {
		return fmt.Errorf("error adding event handlers for pod informer: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.factory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("error syncing pod informer: %w", err)
	}

	return nil
}

func (m *DeploymentPodManager) Stop() error {
	if err := m.imexChannelManager.DeletePool(m.multiNodeEnvironmentLabel); err != nil {
		return fmt.Errorf("error deleting IMEX channel pool: %w", err)
	}
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

func (m *DeploymentPodManager) onPodAddOrUpdate(ctx context.Context, obj any) error {
	p, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("failed to cast to Pod")
	}

	p, err := m.lister.Pods(p.Namespace).Get(p.Name)
	if err != nil && errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("erroring retreiving Pod: %w", err)
	}

	klog.Infof("Processing added or updated Pod: %s/%s", p.Namespace, p.Name)

	if p.Spec.NodeName == "" {
		return fmt.Errorf("pod not yet scheduled: %s/%s", p.Namespace, p.Name)
	}

	hostnameLabels := m.nodeSelector.NodeSelectorTerms[0].MatchExpressions[0].Values
	if !slices.Contains(hostnameLabels, p.Spec.NodeName) {
		hostnameLabels = append(hostnameLabels, p.Spec.NodeName)
	}
	m.nodeSelector.NodeSelectorTerms[0].MatchExpressions[0].Values = hostnameLabels

	if len(hostnameLabels) != m.numPods {
		return fmt.Errorf("node selector not yet complete")
	}

	if err := m.imexChannelManager.CreateOrUpdatePool(m.multiNodeEnvironmentLabel, &m.nodeSelector); err != nil {
		return fmt.Errorf("failed to create or update IMEX channel pool: %w", err)
	}

	return nil
}
