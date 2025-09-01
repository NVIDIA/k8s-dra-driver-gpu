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

	getComputeDomain GetComputeDomainFunc
	computeDomainUID string

	cleanupManager *CleanupManager[*corev1.Pod]
}

func NewDaemonSetPodManager(config *ManagerConfig, labelSelector *metav1.LabelSelector, getComputeDomain GetComputeDomainFunc, computeDomainUID string) *DaemonSetPodManager {
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
		config:           config,
		factory:          factory,
		informer:         informer,
		lister:           lister,
		getComputeDomain: getComputeDomain,
		computeDomainUID: computeDomainUID,
	}

	m.cleanupManager = NewCleanupManager[*corev1.Pod](informer, getComputeDomain, m.cleanup)

	return m
}

// updateComputeDomainNodes updates the ComputeDomain status with new nodes and handles status changes.
func (m *DaemonSetPodManager) updateComputeDomainNodes(ctx context.Context, cd *nvapi.ComputeDomain, updatedNodes []*nvapi.ComputeDomainNode) error {
	// If no nodes were removed, nothing to do
	if len(updatedNodes) == len(cd.Status.Nodes) {
		return nil
	}

	// If the number of nodes is now less than required, set status to NotReady
	if len(updatedNodes) < cd.Spec.NumNodes {
		cd.Status.Status = nvapi.ComputeDomainStatusNotReady
	}

	cd.Status.Nodes = updatedNodes
	if _, err := m.config.clientsets.Nvidia.ResourceV1beta1().ComputeDomains(cd.Namespace).UpdateStatus(ctx, cd, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating ComputeDomain status: %w", err)
	}

	return nil
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

	if err := m.cleanupManager.Start(ctx); err != nil {
		return fmt.Errorf("error starting cleanup manager: %w", err)
	}

	return nil
}

func (m *DaemonSetPodManager) Stop() error {
	if err := m.cleanupManager.Stop(); err != nil {
		return fmt.Errorf("error stopping cleanup manager: %w", err)
	}
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

// cleanup removes nodes from the ComputeDomain status that no longer have backing pods.
func (m *DaemonSetPodManager) cleanup(ctx context.Context, cdUID string) error {
	cd, err := m.getComputeDomain(cdUID)
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		return nil
	}

	pods, err := m.lister.List(nil)
	if err != nil {
		return fmt.Errorf("error listing pods: %w", err)
	}

	newCD := cd.DeepCopy()

	// Create a set of pod IPs for efficient lookup
	podIPs := make(map[string]struct{})
	for _, pod := range pods {
		if pod.Status.PodIP != "" {
			podIPs[pod.Status.PodIP] = struct{}{}
		}
	}

	// Check if any nodes in the ComputeDomain status don't have backing pods
	var updatedNodes []*nvapi.ComputeDomainNode
	for _, node := range newCD.Status.Nodes {
		if _, exists := podIPs[node.IPAddress]; exists {
			updatedNodes = append(updatedNodes, node)
		}
	}

	// Update the ComputeDomain status
	if err := m.updateComputeDomainNodes(ctx, newCD, updatedNodes); err != nil {
		return fmt.Errorf("error updating ComputeDomain status after removing stale nodes: %w", err)
	}

	klog.Infof("Successfully cleaned up stale nodes from ComputeDomain %s/%s", cd.Namespace, cd.Name)
	return nil
}

func (m *DaemonSetPodManager) onPodDelete(ctx context.Context, obj any) error {
	p, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("failed to cast to Pod")
	}

	cd, err := m.getComputeDomain(m.computeDomainUID)
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

	// Update the ComputeDomain status
	if err := m.updateComputeDomainNodes(ctx, newCD, updatedNodes); err != nil {
		return fmt.Errorf("error removing node from ComputeDomain status: %w", err)
	}

	klog.Infof("Successfully removed node with IP %s from ComputeDomain %s/%s", p.Status.PodIP, newCD.Namespace, newCD.Name)

	return nil
}
