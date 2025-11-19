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
	config                     *ManagerConfig
	getComputeDomain           GetComputeDomainFunc
	waitGroup                  sync.WaitGroup
	cancelContext              context.CancelFunc
	podInformer                cache.SharedIndexInformer
	informerFactory            informers.SharedInformerFactory
	computeDomainMutationCache cache.MutationCache
}

// NewPodManager creates a new PodManager instance.
func NewPodManager(config *ManagerConfig, getComputeDomain GetComputeDomainFunc, mutationCache cache.MutationCache) *PodManager {
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
		config:                     config,
		getComputeDomain:           getComputeDomain,
		podInformer:                podInformer,
		informerFactory:            informerFactory,
		computeDomainMutationCache: mutationCache,
	}

	return p
}

// Start starts the pod manager.
func (pm *PodManager) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	pm.cancelContext = cancel

	_, err := pm.podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		// Use `WithKey` with hard-coded key, to cancel any previous update task
		// (we want to make sure that the latest pod status update wins).
		AddFunc: func(obj any) {
			pm.config.workQueue.EnqueueWithKey(obj, "pod", pm.addOrUpdate)
		},
		UpdateFunc: func(oldObj, newObj any) {
			pm.config.workQueue.EnqueueWithKey(newObj, "pod", pm.addOrUpdate)
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
	klog.Infof("Terminating: pod manager")
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
		return fmt.Errorf("pod update: failed to update note status in CD (%s): %w", status, err)
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

// updateNodeStatus updates the status of the current node (the status of the
// pod running the CD daemon) in the CD status.
func (pm *PodManager) updateNodeStatus(ctx context.Context, status string) error {
	// Get the current CD using the provided function
	cd, err := pm.getComputeDomain(pm.config.computeDomainUUID)
	if err != nil {
		return fmt.Errorf("failed to get ComputeDomain: %w", err)
	}
	if cd == nil {
		return fmt.Errorf("ComputeDomain '%s/%s' not found", pm.config.computeDomainName, pm.config.computeDomainUUID)
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

	// If node not found, exit early. Here, we could also assert `status ==
	// NotReady`, assumption: the CD daemon only starts after the CD manager has
	// performed the node info insert (leading to node != nil below), and the
	// pod can only change its status to Ready after the CD daemon has started.
	// Return explicit error that is being retried, and rely on the retry chain
	// to be canceled by a newer incoming pod update).
	if node == nil {
		return fmt.Errorf("node not yet listed in CD (waiting for insertion)")
	}

	// If status hasn't changed, exit early
	if node.Status == status {
		klog.V(6).Infof("updateNodeStatus noop: status not changed (%s)", status)
		return nil
	}

	// Update the node status to the new value
	node.Status = status

	// Update the CD status. Store the CD object returned by the API server in
	// the mutationCache (it now has a newer ResourceVersion than `newCD` and
	// hence any further mutations should be performed based on that).
	newCD, err = pm.config.clientsets.Nvidia.ResourceV1beta1().ComputeDomains(newCD.Namespace).UpdateStatus(ctx, newCD, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("error updating node status in ComputeDomain: %w", err)
	}
	pm.computeDomainMutationCache.Mutation(newCD)

	klog.Infof("Successfully updated node status in CD (new nodeinfo: %v)", node)
	return nil
}
