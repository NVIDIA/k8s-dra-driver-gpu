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
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

type DaemonSetPodManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory       informers.SharedInformerFactory
	informer      cache.SharedIndexInformer
	lister        corev1listers.PodLister
	mutationCache cache.MutationCache
}

func NewDaemonSetPodManager(config *ManagerConfig) *DaemonSetPodManager {
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

	informer := factory.Core().V1().Pods().Informer()
	lister := factory.Core().V1().Pods().Lister()

	m := &DaemonSetPodManager{
		config:   config,
		factory:  factory,
		informer: informer,
		lister:   lister,
	}

	return m
}

func (m *DaemonSetPodManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping DaemonSetPod manager: %v", err)
			}
		}
	}()

	if err := addComputeDomainLabelIndexer[*corev1.Pod](m.informer); err != nil {
		return fmt.Errorf("error adding indexer for MultiNodeEnvironment label: %w", err)
	}

	m.mutationCache = cache.NewIntegerResourceVersionMutationCache(
		klog.Background(),
		m.informer.GetStore(),
		m.informer.GetIndexer(),
		mutationCacheTTL,
		true,
	)

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.factory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for Pods failed")
	}

	return nil
}

func (m *DaemonSetPodManager) Stop() error {
	if m.cancelContext != nil {
		m.cancelContext()
	}
	m.waitGroup.Wait()
	return nil
}

// List returns all daemon pods from the informer cache.
func (m *DaemonSetPodManager) List() ([]*corev1.Pod, error) {
	return m.lister.Pods(m.config.driverNamespace).List(labels.Everything())
}

func (m *DaemonSetPodManager) Get(ctx context.Context, cdUID string, nodeName string) (*corev1.Pod, error) {
	pods, err := getByComputeDomainUID[*corev1.Pod](ctx, m.mutationCache, cdUID)
	if err != nil {
		return nil, fmt.Errorf("error retrieving DaemonSetPods: %w", err)
	}

	for _, p := range pods {
		if p.Spec.NodeName == nodeName {
			return p, nil
		}
	}

	return nil, fmt.Errorf("no DeamonSetPod with name %s matching DomainID %s found", nodeName, cdUID)
}
