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
	"k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
)

type DaemonSetPodManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer
	lister   corev1listers.PodLister

	getComputeDomain          GetComputeDomainFunc
	updateComputeDomainStatus UpdateComputeDomainStatusFunc
}

func NewDaemonSetPodManager(config *ManagerConfig, getComputeDomain GetComputeDomainFunc, updateComputeDomainStatus UpdateComputeDomainStatusFunc) *DaemonSetPodManager {
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
		config:                    config,
		factory:                   factory,
		informer:                  informer,
		lister:                    lister,
		getComputeDomain:          getComputeDomain,
		updateComputeDomainStatus: updateComputeDomainStatus,
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

	_, err := m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			m.config.workQueue.Enqueue(obj, m.onPodAddOrUpdate)
		},
		UpdateFunc: func(objOld, objNew interface{}) {
			m.config.workQueue.Enqueue(objNew, m.onPodAddOrUpdate)
		},
		DeleteFunc: func(obj interface{}) {
			m.config.workQueue.Enqueue(obj, m.onPodDelete)
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

func (m *DaemonSetPodManager) Stop() error {
	if m.cancelContext != nil {
		m.cancelContext()
	}
	m.waitGroup.Wait()
	return nil
}

func (m *DaemonSetPodManager) onPodAddOrUpdate(ctx context.Context, obj any) error {
	p, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("failed to cast to Pod")
	}

	// Skip pods that aren't scheduled yet
	if p.Status.PodIP == "" || p.Spec.NodeName == "" {
		klog.V(6).Infof("Skipping pod %s/%s: not scheduled (IP=%s, Node=%s)", p.Namespace, p.Name, p.Status.PodIP, p.Spec.NodeName)
		return nil
	}

	// Skip pods that aren't running
	if p.Status.Phase != corev1.PodRunning {
		klog.V(6).Infof("Skipping pod %s/%s: not running (phase=%s)", p.Namespace, p.Name, p.Status.Phase)
		return nil
	}

	cd, err := m.getComputeDomain(p.Labels[computeDomainLabelKey])
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		return nil
	}

	// Get the node to retrieve the cliqueID label
	node, err := m.config.clientsets.Core.CoreV1().Nodes().Get(ctx, p.Spec.NodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting node %s: %w", p.Spec.NodeName, err)
	}

	cliqueID := node.Labels[computeDomainCliqueIDLabelKey]

	// Determine the desired node status based on pod readiness
	nodeStatus := nvapi.ComputeDomainStatusNotReady
	if isPodReady(p) {
		nodeStatus = nvapi.ComputeDomainStatusReady
	}

	klog.V(4).Infof("Processing pod %s/%s: Phase=%s, Ready=%v, NodeStatus=%s", p.Namespace, p.Name, p.Status.Phase, isPodReady(p), nodeStatus)

	// Ensure node info in ComputeDomain status
	if err := m.ensureNodeInfoInCD(ctx, cd, p.Spec.NodeName, p.Status.PodIP, cliqueID, nodeStatus); err != nil {
		return fmt.Errorf("error ensuring node info in ComputeDomain: %w", err)
	}

	return nil
}

func (m *DaemonSetPodManager) onPodDelete(ctx context.Context, obj any) error {
	p, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("failed to cast to Pod")
	}

	cd, err := m.getComputeDomain(p.Labels[computeDomainLabelKey])
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		return nil
	}

	newCD := cd.DeepCopy()

	// Filter out the node with the current pod's IP address
	var updatedNodes []*nvapi.ComputeDomainNode
	for _, node := range newCD.Status.Nodes {
		if node.IPAddress != p.Status.PodIP {
			updatedNodes = append(updatedNodes, node)
		}
	}

	// If no nodes were removed, nothing to do
	if len(updatedNodes) == len(newCD.Status.Nodes) {
		return nil
	}

	// If the number of nodes is now less than required, set status to NotReady
	if len(updatedNodes) < newCD.Spec.NumNodes {
		newCD.Status.Status = nvapi.ComputeDomainStatusNotReady
	}

	newCD.Status.Nodes = updatedNodes
	if _, err := m.updateComputeDomainStatus(ctx, newCD); err != nil {
		return fmt.Errorf("error removing node from ComputeDomain status: %w", err)
	}

	klog.Infof("Successfully removed node with IP %s from ComputeDomain %s/%s", p.Status.PodIP, newCD.Namespace, newCD.Name)

	return nil
}

// ensureNodeInfoInCD ensures that the node is represented in the ComputeDomain status
// and that its IP address and status are up to date.
func (m *DaemonSetPodManager) ensureNodeInfoInCD(ctx context.Context, cd *nvapi.ComputeDomain, nodeName string, podIP string, cliqueID string, nodeStatus string) error {
	var nodeInfo *nvapi.ComputeDomainNode

	// Create a deep copy to avoid modifying the original
	newCD := cd.DeepCopy()

	// Try to find an existing entry with the pod's node name
	for _, node := range newCD.Status.Nodes {
		if node.Name == nodeName {
			nodeInfo = node
			break
		}
	}

	// If there is one and both IP and status are the same, we are done
	if nodeInfo != nil && nodeInfo.IPAddress == podIP && nodeInfo.Status == nodeStatus {
		klog.V(6).Infof("ensureNodeInfoInCD noop: pod IP and status unchanged (IP=%s, Status=%s) for node %s", podIP, nodeStatus, nodeName)
		return nil
	}

	// If there isn't one, create one and append it to the list
	if nodeInfo == nil {
		// Get the next available index for this new node
		nextIndex, err := getNextAvailableIndex(cliqueID, newCD.Status.Nodes, m.config.maxNodesPerIMEXDomain)
		if err != nil {
			return fmt.Errorf("error getting next available index: %w", err)
		}

		nodeInfo = &nvapi.ComputeDomainNode{
			Name:     nodeName,
			CliqueID: cliqueID,
			Index:    nextIndex,
		}

		klog.Infof("CD status does not contain node name '%s' yet, inserting: %v", nodeName, nodeInfo)
		newCD.Status.Nodes = append(newCD.Status.Nodes, nodeInfo)
	}

	// Unconditionally update its IP address and status
	nodeInfo.IPAddress = podIP
	nodeInfo.Status = nodeStatus

	// Conditionally update global CD status if it's still in its initial status
	if newCD.Status.Status == "" {
		newCD.Status.Status = nvapi.ComputeDomainStatusNotReady
	}

	// Update status using the shared mutation cache
	if _, err := m.updateComputeDomainStatus(ctx, newCD); err != nil {
		return fmt.Errorf("error updating ComputeDomain status: %w", err)
	}

	klog.Infof("Successfully inserted/updated node %s in CD (nodeinfo: %v)", nodeName, nodeInfo)
	return nil
}

// isPodReady determines if a pod is ready based on its conditions.
func isPodReady(pod *corev1.Pod) bool {
	// Check if the pod is in Running phase
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}

	// Check if all containers are ready
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return false
}

// getNextAvailableIndex finds the next available index for the current node by
// seeing which ones are already taken by other nodes in the ComputeDomain
// status that have the same cliqueID. It fills in gaps where it can, and returns
// an error if no index is available within maxNodesPerIMEXDomain.
func getNextAvailableIndex(currentCliqueID string, nodes []*nvapi.ComputeDomainNode, maxNodesPerIMEXDomain int) (int, error) {
	// Filter nodes to only consider those with the same cliqueID
	var cliqueNodes []*nvapi.ComputeDomainNode
	for _, node := range nodes {
		if node.CliqueID == currentCliqueID {
			cliqueNodes = append(cliqueNodes, node)
		}
	}

	// Create a map to track used indices
	usedIndices := make(map[int]bool)

	// Collect all currently used indices from nodes with the same cliqueID
	for _, node := range cliqueNodes {
		usedIndices[node.Index] = true
	}

	// Find the next available index, starting from 0 and filling gaps
	nextIndex := 0
	for usedIndices[nextIndex] {
		nextIndex++
	}

	// Skip maxNodesPerIMEXDomain check in the special case of no clique ID
	// being set: this means that this node does not actually run an IMEX daemon
	if currentCliqueID == "" {
		return nextIndex, nil
	}

	// Ensure nextIndex is within the range 0..maxNodesPerIMEXDomain
	if nextIndex < 0 || nextIndex >= maxNodesPerIMEXDomain {
		return -1, fmt.Errorf("no available indices within maxNodesPerIMEXDomain (%d) for cliqueID %s", maxNodesPerIMEXDomain, currentCliqueID)
	}

	return nextIndex, nil
}
