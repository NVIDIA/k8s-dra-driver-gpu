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
	"strconv"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const (
	// cliqueConfigMapPrefix is the prefix for ConfigMaps that store clique node-to-index mappings.
	cliqueConfigMapPrefix = "clique-"
)

// CliqueConfigMapManager watches kubelet plugin pods and maintains ConfigMaps
// with node-name to index mappings for each clique.
type CliqueConfigMapManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer
}

// NewCliqueConfigMapManager creates a new CliqueConfigMapManager that watches
// kubelet plugin pods with the computeDomainCliqueLabelKey label.
func NewCliqueConfigMapManager(config *ManagerConfig) *CliqueConfigMapManager {
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      computeDomainCliqueLabelKey,
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

	m := &CliqueConfigMapManager{
		config:   config,
		factory:  factory,
		informer: informer,
	}

	return m
}

// Start begins watching kubelet plugin pods and managing clique ConfigMaps.
func (m *CliqueConfigMapManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping CliqueConfigMap manager: %v", err)
			}
		}
	}()

	_, err := m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			m.config.workQueue.Enqueue(obj, m.onPodAddOrUpdate)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			m.config.workQueue.Enqueue(newObj, m.onPodAddOrUpdate)
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
		return fmt.Errorf("error syncing pod informer cache")
	}

	return nil
}

// Stop stops the CliqueConfigMapManager.
func (m *CliqueConfigMapManager) Stop() error {
	if m.cancelContext != nil {
		m.cancelContext()
	}
	m.waitGroup.Wait()
	return nil
}

// onPodAddOrUpdate handles pod add/update events by ensuring the node is in the
// clique's ConfigMap with a stable index.
func (m *CliqueConfigMapManager) onPodAddOrUpdate(ctx context.Context, obj any) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("failed to cast to Pod")
	}

	nodeName := pod.Spec.NodeName
	if nodeName == "" {
		return nil
	}

	cliqueID := pod.Labels[computeDomainCliqueLabelKey]
	if cliqueID == "" {
		return nil
	}

	return m.ensureNodeInConfigMap(ctx, cliqueID, nodeName)
}

// ensureNodeInConfigMap ensures a node is present in the clique's ConfigMap.
// If the node is already present, its index is preserved.
// If the node is new, it gets the lowest available index.
func (m *CliqueConfigMapManager) ensureNodeInConfigMap(ctx context.Context, cliqueID, nodeName string) error {
	cmName := cliqueConfigMapPrefix + cliqueID

	cm, err := m.config.clientsets.Core.CoreV1().ConfigMaps(m.config.driverNamespace).Get(ctx, cmName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return m.createConfigMap(ctx, cmName, nodeName)
	}
	if err != nil {
		return fmt.Errorf("get ConfigMap: %w", err)
	}

	if _, exists := cm.Data[nodeName]; exists {
		return nil
	}

	return m.addNodeToConfigMap(ctx, cm, nodeName)
}

// createConfigMap creates a new ConfigMap with the given node at index 0.
func (m *CliqueConfigMapManager) createConfigMap(ctx context.Context, cmName, nodeName string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: m.config.driverNamespace,
		},
		Data: map[string]string{
			nodeName: "0",
		},
	}

	if _, err := m.config.clientsets.Core.CoreV1().ConfigMaps(m.config.driverNamespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("create ConfigMap: %w", err)
	}

	klog.Infof("Created ConfigMap %s", cmName)
	return nil
}

// addNodeToConfigMap adds a node to an existing ConfigMap with the next available index.
func (m *CliqueConfigMapManager) addNodeToConfigMap(ctx context.Context, cm *corev1.ConfigMap, nodeName string) error {
	nextIndex, err := m.getNextAvailableIndex(cm.Data)
	if err != nil {
		return fmt.Errorf("get next available index: %w", err)
	}

	newCM := cm.DeepCopy()
	if newCM.Data == nil {
		newCM.Data = make(map[string]string)
	}
	newCM.Data[nodeName] = strconv.Itoa(nextIndex)

	if _, err := m.config.clientsets.Core.CoreV1().ConfigMaps(m.config.driverNamespace).Update(ctx, newCM, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update ConfigMap: %w", err)
	}

	klog.Infof("Added node %s to ConfigMap %s at index %d", nodeName, cm.Name, nextIndex)
	return nil
}

// getNextAvailableIndex finds the lowest available index not currently assigned.
// Returns an error if no index is available within maxNodesPerIMEXDomain.
func (m *CliqueConfigMapManager) getNextAvailableIndex(data map[string]string) (int, error) {
	usedIndices := make(map[int]bool)

	for _, indexStr := range data {
		if index, err := strconv.Atoi(indexStr); err == nil {
			usedIndices[index] = true
		}
	}

	// Find the next available index, starting from 0 and filling gaps
	nextIndex := 0
	for usedIndices[nextIndex] {
		nextIndex++
	}

	if nextIndex >= m.config.maxNodesPerIMEXDomain {
		return -1, fmt.Errorf("no available index within maxNodesPerIMEXDomain (%d)", m.config.maxNodesPerIMEXDomain)
	}

	return nextIndex, nil
}
