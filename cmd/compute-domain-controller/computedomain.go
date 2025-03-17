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
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	nvinformers "github.com/NVIDIA/k8s-dra-driver-gpu/pkg/nvidia.com/informers/externalversions"
)

type GetComputeDomainFunc func(uid string) (*nvapi.ComputeDomain, error)

const (
	informerResyncPeriod = 10 * time.Minute

	computeDomainLabelKey  = "resource.nvidia.com/computeDomain"
	computeDomainFinalizer = computeDomainLabelKey

	computeDomainDefaultChannelDeviceClass = "compute-domain-default-channel.nvidia.com"
	computeDomainChannelDeviceClass        = "compute-domain-channel.nvidia.com"
	computeDomainDaemonDeviceClass         = "compute-domain-daemon.nvidia.com"

	computeDomainResourceClaimTemplateTargetLabelKey = "resource.nvidia.com/computeDomainTarget"
	computeDomainResourceClaimTemplateTargetDaemon   = "Daemon"
	computeDomainResourceClaimTemplateTargetWorkload = "Workload"
)

type ComputeDomainManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  nvinformers.SharedInformerFactory
	informer cache.SharedIndexInformer

	daemonSetManager             *DaemonSetManager
	resourceClaimTemplateManager *WorkloadResourceClaimTemplateManager
}

// NewComputeDomainManager creates a new ComputeDomainManager.
func NewComputeDomainManager(config *ManagerConfig) *ComputeDomainManager {
	factory := nvinformers.NewSharedInformerFactory(config.clientsets.Nvidia, informerResyncPeriod)
	informer := factory.Resource().V1beta1().ComputeDomains().Informer()

	klog.Infof("Creating new ComputeDomainManager for %s/%s", config.driverName, config.driverNamespace)
	m := &ComputeDomainManager{
		config:   config,
		factory:  factory,
		informer: informer,
	}
	// TODO (swati) add logs for daemonset and resourceClaimTemplate managers in verbose mode
	m.daemonSetManager = NewDaemonSetManager(config, m.Get)
	m.resourceClaimTemplateManager = NewWorkloadResourceClaimTemplateManager(config, m.Get)

	return m
}

// Start starts a ComputeDomainManager.
func (m *ComputeDomainManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping ComputeDomain manager: %v", err)
			}
		}
	}()

	err := m.informer.AddIndexers(cache.Indexers{
		"uid": uidIndexer[*nvapi.ComputeDomain],
	})
	if err != nil {
		return fmt.Errorf("error adding indexer for UIDs: %w", err)
	}

	_, err = m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			m.config.workQueue.Enqueue(obj, m.onAddOrUpdate)
		},
		UpdateFunc: func(oldObj, newObj any) {
			m.config.workQueue.Enqueue(newObj, m.onAddOrUpdate)
		},
	})
	if err != nil {
		return fmt.Errorf("error adding event handlers for ComputeDomain informer: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.factory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for ComputeDomains failed")
	}

	if err := m.daemonSetManager.Start(ctx); err != nil {
		return fmt.Errorf("error starting DaemonSet manager: %w", err)
	}

	if err := m.resourceClaimTemplateManager.Start(ctx); err != nil {
		return fmt.Errorf("error creating ResourceClaim manager: %w", err)
	}

	return nil
}

func (m *ComputeDomainManager) Stop() error {
	if err := m.daemonSetManager.Stop(); err != nil {
		return fmt.Errorf("error stopping DaemonSet manager: %w", err)
	}
	if err := m.resourceClaimTemplateManager.Stop(); err != nil {
		return fmt.Errorf("error stopping ResourceClaimTemplate manager: %w", err)
	}
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

// Get gets a ComputeDomain with a specific UID.
func (m *ComputeDomainManager) Get(uid string) (*nvapi.ComputeDomain, error) {
	cds, err := m.informer.GetIndexer().ByIndex("uid", uid)
	if err != nil {
		return nil, fmt.Errorf("error retrieving ComputeDomain by UID: %w", err)
	}
	if len(cds) == 0 {
		klog.V(2).Infof("No ComputeDomain found with UID: %s", uid)
		return nil, nil
	}
	if len(cds) != 1 {
		return nil, fmt.Errorf("multiple ComputeDomains with the same UID")
	}
	cd, ok := cds[0].(*nvapi.ComputeDomain)
	if !ok {
		return nil, fmt.Errorf("failed to cast to ComputeDomain")
	}
	return cd, nil
}

// RemoveFinalizer removes the finalizer from a ComputeDomain.
func (m *ComputeDomainManager) RemoveFinalizer(ctx context.Context, uid string) error {
	cd, err := m.Get(uid)
	if err != nil {
		return fmt.Errorf("error retrieving ComputeDomain: %w", err)
	}
	if cd == nil {
		klog.V(2).Infof("ComputeDomain with UID %s not found, nothing to do", uid)
		return nil
	}

	if cd.GetDeletionTimestamp() == nil {
		return fmt.Errorf("attempting to remove finalizer before ComputeDomain %s/%s with UID %s marked for deletion", cd.Namespace, cd.Name, uid)
	}

	newCD := cd.DeepCopy()
	newCD.Finalizers = []string{}
	for _, f := range cd.Finalizers {
		if f != computeDomainFinalizer {
			newCD.Finalizers = append(newCD.Finalizers, f)
		}
	}
	if len(cd.Finalizers) == len(newCD.Finalizers) {
		klog.V(2).Infof("Finalizer not found on ComputeDomain %s/%s, nothing to do", cd.Namespace, cd.Name)
		return nil
	}

	if _, err = m.config.clientsets.Nvidia.ResourceV1beta1().ComputeDomains(cd.Namespace).Update(ctx, newCD, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating ComputeDomain: %w", err)
	}

	return nil
}

// logNodesWithComputeDomainLabel logs nodes that have a ComputeDomain label and returns their names.
func (m *ComputeDomainManager) logNodesWithComputeDomainLabel(nodes *corev1.NodeList, cdUID string) []string {
	if len(nodes.Items) == 0 {
		klog.Infof("No nodes found with label for ComputeDomain with UID %s", cdUID)
		return nil
	}

	nodeNames := []string{}
	for _, node := range nodes.Items {
		nodeNames = append(nodeNames, node.Name)
	}
	return nodeNames
}

// AssertWorkloadsCompletes ensures that all workloads asssociated with a ComputeDomain have completed.
//
// TODO: We should probably also check to ensure that all ResourceClaims
// generated from our ResourceClaimTemplate for workloads are gone. Doing
// something is better than nothing for now though.
func (m *ComputeDomainManager) AssertWorkloadsCompleted(ctx context.Context, cdUID string) error {
	labelSelector := &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      computeDomainLabelKey,
				Operator: metav1.LabelSelectorOpIn,
				Values:   []string{cdUID},
			},
		},
	}

	nodes, err := m.config.clientsets.Core.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: metav1.FormatLabelSelector(labelSelector),
	})
	if err != nil {
		return fmt.Errorf("error retrieving nodes: %w", err)
	}

	if len(nodes.Items) != 0 {
		nodeNames := m.logNodesWithComputeDomainLabel(nodes, cdUID)
		return fmt.Errorf("nodes %v with label for ComputeDomain %s", nodeNames, cdUID)
	}
	return nil
}

func (m *ComputeDomainManager) addFinalizer(ctx context.Context, cd *nvapi.ComputeDomain) error {
	for _, f := range cd.Finalizers {
		if f == computeDomainFinalizer {
			return nil
		}
	}

	newCD := cd.DeepCopy()
	newCD.Finalizers = append(newCD.Finalizers, computeDomainFinalizer)
	if _, err := m.config.clientsets.Nvidia.ResourceV1beta1().ComputeDomains(cd.Namespace).Update(ctx, newCD, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating ComputeDomain: %w", err)
	}

	return nil
}

func (m *ComputeDomainManager) onAddOrUpdate(ctx context.Context, obj any) error {
	cd, ok := obj.(*nvapi.ComputeDomain)
	if !ok {
		return fmt.Errorf("failed to cast to ComputeDomain")
	}

	klog.Infof("Processing added or updated ComputeDomain: %s/%s", cd.Namespace, cd.Name)

	if cd.GetDeletionTimestamp() != nil {
		if err := m.resourceClaimTemplateManager.Delete(ctx, string(cd.UID)); err != nil {
			return fmt.Errorf("error deleting ResourceClaimTemplate: %w", err)
		}

		if err := m.daemonSetManager.Delete(ctx, string(cd.UID)); err != nil {
			return fmt.Errorf("error deleting DaemonSet: %w", err)
		}

		if err := m.AssertWorkloadsCompleted(ctx, string(cd.UID)); err != nil {
			return fmt.Errorf("error asserting workloads completed: %w", err)
		}

		if err := m.resourceClaimTemplateManager.RemoveFinalizer(ctx, string(cd.UID)); err != nil {
			return fmt.Errorf("error removing finalizer on ResourceClaimTemplate: %w", err)
		}

		if err := m.resourceClaimTemplateManager.AssertRemoved(ctx, string(cd.UID)); err != nil {
			return fmt.Errorf("error asserting removal of ResourceClaimTemplate: %w", err)
		}

		if err := m.daemonSetManager.RemoveFinalizer(ctx, string(cd.UID)); err != nil {
			return fmt.Errorf("error removing finalizer on DaemonSet: %w", err)
		}

		if err := m.daemonSetManager.AssertRemoved(ctx, string(cd.UID)); err != nil {
			return fmt.Errorf("error asserting removal of DaemonSet: %w", err)
		}

		if err := m.RemoveFinalizer(ctx, string(cd.UID)); err != nil {
			return fmt.Errorf("error removing finalizer: %w", err)
		}

		return nil
	}

	if err := m.addFinalizer(ctx, cd); err != nil {
		return fmt.Errorf("error adding finalizer: %w", err)
	}

	if _, err := m.daemonSetManager.Create(ctx, m.config.driverNamespace, cd); err != nil {
		return fmt.Errorf("error creating DaemonSet: %w", err)
	}

	if _, err := m.resourceClaimTemplateManager.Create(ctx, cd.Namespace, cd.Spec.Channel.ResourceClaimTemplate.Name, cd); err != nil {
		return fmt.Errorf("error creating ResourceClaimTemplate '%s/%s': %w", cd.Namespace, cd.Spec.Channel.ResourceClaimTemplate.Name, err)
	}

	return nil
}
