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
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/workqueue"
)

const (
	// cliqueConfigMapPrefix is the prefix for ConfigMaps that store clique node-to-index mappings.
	cliqueConfigMapPrefix = "clique-"
)

// CliqueConfigMapManager watches kubelet plugin pods and maintains ConfigMaps
// with node-name to index mappings for each clique.
type CliqueConfigMapManager struct {
	config        *ManagerConfig
	workQueue     *workqueue.WorkQueue
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
	workQueue := workqueue.New(workqueue.DefaultControllerRateLimiter())

	m := &CliqueConfigMapManager{
		config:    config,
		workQueue: workQueue,
		factory:   factory,
		informer:  informer,
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
			m.workQueue.Enqueue(obj, m.onPodAddOrUpdate)
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			m.workQueue.Enqueue(newObj, m.onPodAddOrUpdate)
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

	// Start periodic cleanup goroutine
	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.periodicCleanup(ctx)
	}()

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.workQueue.Run(ctx)
	}()

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

// periodicCleanup periodically removes stale entries from clique ConfigMaps.
func (m *CliqueConfigMapManager) periodicCleanup(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	// Run cleanup immediately on startup
	m.cleanupStaleEntries(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.cleanupStaleEntries(ctx)
		}
	}
}

// cleanupStaleEntries removes entries for nodes that no longer exist in the cluster
// or have a kubelet plugin pod with a different clique label,
// and deletes ConfigMaps that become empty.
func (m *CliqueConfigMapManager) cleanupStaleEntries(ctx context.Context) {
	klog.V(6).Infof("Cleanup: checking for stale clique ConfigMap entries")

	// Get the set of nodes that exist in the cluster
	nodes, err := m.getNodes(ctx)
	if err != nil {
		klog.Errorf("error getting nodes for cleanup: %v", err)
		return
	}

	// List all ConfigMaps in the driver namespace
	cmList, err := m.config.clientsets.Core.CoreV1().ConfigMaps(m.config.driverNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Errorf("error listing ConfigMaps for cleanup: %v", err)
		return
	}

	for _, cm := range cmList.Items {
		// Only consider ConfigMaps with the clique prefix
		if !strings.HasPrefix(cm.Name, cliqueConfigMapPrefix) {
			continue
		}

		m.cleanupConfigMap(ctx, &cm, nodes)
	}
}

// cleanupConfigMap removes entries for nodes that no longer exist or have a kubelet
// plugin pod with a different clique label value.
func (m *CliqueConfigMapManager) cleanupConfigMap(ctx context.Context, cm *corev1.ConfigMap, nodes sets.Set[string]) {
	cliqueID := strings.TrimPrefix(cm.Name, cliqueConfigMapPrefix)
	cliqueLabels := m.getCliqueLabels()

	var nodesToRemove []string
	for nodeName := range cm.Data {
		if m.shouldRemoveNode(nodeName, cliqueID, nodes, cliqueLabels) {
			nodesToRemove = append(nodesToRemove, nodeName)
		}
	}

	if len(nodesToRemove) == 0 {
		return
	}

	newCM := cm.DeepCopy()
	for _, nodeName := range nodesToRemove {
		klog.Infof("Cleanup: removing node %s from ConfigMap %s", nodeName, cm.Name)
		delete(newCM.Data, nodeName)
	}

	// If ConfigMap is now empty, delete it
	if len(newCM.Data) == 0 {
		klog.Infof("Cleanup: deleting empty ConfigMap %s", cm.Name)
		if err := m.config.clientsets.Core.CoreV1().ConfigMaps(m.config.driverNamespace).Delete(ctx, cm.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			klog.Errorf("error deleting empty ConfigMap %s: %v", cm.Name, err)
		}
		return
	}

	// Update the ConfigMap with removed entries
	if _, err := m.config.clientsets.Core.CoreV1().ConfigMaps(m.config.driverNamespace).Update(ctx, newCM, metav1.UpdateOptions{}); err != nil {
		klog.Errorf("error updating ConfigMap %s after cleanup: %v", cm.Name, err)
	}
}

// shouldRemoveNode returns true if a node entry should be removed from a clique ConfigMap.
// A node should be removed if:
// - The node no longer exists in the cluster.
// - The node has a kubelet plugin pod with a different clique label (no label is OK).
func (m *CliqueConfigMapManager) shouldRemoveNode(nodeName, cliqueID string, nodes sets.Set[string], cliqueLabels map[string]string) bool {
	if !nodes.Has(nodeName) {
		return true
	}
	if _, exists := cliqueLabels[nodeName]; !exists {
		return false
	}
	if cliqueLabels[nodeName] != cliqueID {
		return true
	}
	return false
}

// getNodes returns a set of node names that exist in the cluster.
func (m *CliqueConfigMapManager) getNodes(ctx context.Context) (sets.Set[string], error) {
	nodeList, err := m.config.clientsets.Core.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	nodes := sets.New[string]()
	for _, node := range nodeList.Items {
		nodes.Insert(node.Name)
	}

	return nodes, nil
}

// getCliqueLabels returns a mapping of node name to cliqueID for nodes that have
// kubelet plugin pods with the clique label, using the informer cache.
func (m *CliqueConfigMapManager) getCliqueLabels() map[string]string {
	cliqueLabels := make(map[string]string)

	for _, obj := range m.informer.GetStore().List() {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		cliqueID := pod.Labels[computeDomainCliqueLabelKey]
		if cliqueID == "" {
			continue
		}
		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			continue
		}
		cliqueLabels[nodeName] = cliqueID
	}

	return cliqueLabels
}
