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
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
)

// PodManager watches for changes to its own pod and updates the CD status accordingly.
type PodManager struct {
	config           *ManagerConfig
	getComputeDomain GetComputeDomainFunc
	waitGroup        sync.WaitGroup
	cancelContext    context.CancelFunc
	podInformer      cache.SharedIndexInformer
	informerFactory  informers.SharedInformerFactory
}

// NewPodManager creates a new PodManager instance.
func NewPodManager(config *ManagerConfig, getComputeDomain GetComputeDomainFunc) *PodManager {
	informerFactory := informers.NewSharedInformerFactoryWithOptions(
		config.clientsets.Core,
		informerResyncPeriod,
		informers.WithNamespace(config.podNamespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fmt.Sprintf("metadata.name=%s", config.podName)
		}),
	)

	podInformer := informerFactory.Core().V1().Pods().Informer()

	p := &PodManager{
		config:           config,
		getComputeDomain: getComputeDomain,
		podInformer:      podInformer,
		informerFactory:  informerFactory,
	}

	return p
}

// Start starts the pod manager.
func (pm *PodManager) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	pm.cancelContext = cancel

	_, err := pm.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			pm.config.workQueue.Enqueue(obj, pm.addOrUpdate)
		},
		UpdateFunc: func(oldObj, newObj any) {
			pm.config.workQueue.Enqueue(newObj, pm.addOrUpdate)
		},
	})
	if err != nil {
		return fmt.Errorf("error adding event handlers for Pod informer: %w", err)
	}

	pm.waitGroup.Add(1)
	go func() {
		defer pm.waitGroup.Done()
		pm.informerFactory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), pm.podInformer.HasSynced) {
		return fmt.Errorf("informer cache sync for Pod failed")
	}

	return nil
}

// Stop stops the pod manager.
func (pm *PodManager) Stop() error {
	// Create a new context for cleanup operations since the original context might be cancelled
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Attempt to update the node status to NotReady
	// Don't return error here as we still want to proceed with shutdown
	if err := pm.updateNodeStatus(ctx, nvapi.ComputeDomainStatusNotReady); err != nil {
		klog.Errorf("Failed to mark node as NotReady during shutdown: %v", err)
	}

	if pm.cancelContext != nil {
		pm.cancelContext()
	}

	pm.waitGroup.Wait()
	return nil
}

// addOrUpdate handles pod add/update events and updates the CD status accordingly.
func (pm *PodManager) addOrUpdate(ctx context.Context, obj any) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("failed to cast object to Pod")
	}

	status := nvapi.ComputeDomainStatusNotReady
	if pm.isPodReady(pod) {
		status = nvapi.ComputeDomainStatusReady
	}

	if err := pm.updateNodeStatus(ctx, status); err != nil {
		return fmt.Errorf("failed to update node status: %w", err)
	}

	return nil
}

// isPodReady determines if a pod is ready based on its conditions.
func (pm *PodManager) isPodReady(pod *corev1.Pod) bool {
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

// updateNodeStatus updates the status of the current node in the CD status.
func (pm *PodManager) updateNodeStatus(ctx context.Context, status string) error {
	// Get the current CD using the provided function
	cd, err := pm.getComputeDomain(pm.config.computeDomainUUID)
	if err != nil {
		return fmt.Errorf("failed to get ComputeDomain: %w", err)
	}
	if cd == nil {
		return fmt.Errorf("ComputeDomain not found")
	}

	// Create a deep copy to avoid modifying the original
	newCD := cd.DeepCopy()

	// Find the node
	var node *nvapi.ComputeDomainNode
	for _, n := range newCD.Status.Nodes {
		if n.Name == pm.config.nodeName {
			node = n
			break
		}
	}

	// If node not found, exit early
	if node == nil {
		return nil
	}

	// If status hasn't changed, exit early
	if node.Status == status {
		return nil
	}

	// Update the node status to the new value
	node.Status = status

	// Update the CD status
	if _, err := pm.config.clientsets.Nvidia.ResourceV1beta1().ComputeDomains(newCD.Namespace).UpdateStatus(ctx, newCD, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating node status in ComputeDomain: %w", err)
	}

	klog.Infof("Successfully updated node %s status to %s", pm.config.nodeName, status)
	return nil
}
