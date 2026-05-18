/*
 * Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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
	"maps"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	nvinformers "github.com/NVIDIA/k8s-dra-driver-gpu/pkg/nvidia.com/informers/externalversions"
)

const (
	informerResyncPeriod = 10 * time.Minute
)

type IPSet map[string]struct{}

// ComputeDomainManager watches compute domains and updates their status with
// info about the ComputeDomain daemon running on this node.
type ComputeDomainManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  nvinformers.SharedInformerFactory
	informer cache.SharedIndexInformer

	previousNodes    []*nvapi.ComputeDomainNode
	updatedNodesChan chan []*nvapi.ComputeDomainNode
}

// NewComputeDomainManager creates a new ComputeDomainManager instance.
func NewComputeDomainManager(config *ManagerConfig) *ComputeDomainManager {
	factory := nvinformers.NewSharedInformerFactoryWithOptions(
		config.clientsets.Nvidia,
		informerResyncPeriod,
		nvinformers.WithNamespace(config.computeDomainNamespace),
		nvinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fmt.Sprintf("metadata.name=%s", config.computeDomainName)
		}),
	)
	informer := factory.Resource().V1beta1().ComputeDomains().Informer()

	m := &ComputeDomainManager{
		config:           config,
		factory:          factory,
		informer:         informer,
		previousNodes:    []*nvapi.ComputeDomainNode{},
		updatedNodesChan: make(chan []*nvapi.ComputeDomainNode),
	}

	return m
}

// Start starts the compute domain manager.
func (m *ComputeDomainManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping ComputeDomainManager: %v", err)
			}
		}
	}()

	err := m.informer.AddIndexers(cache.Indexers{
		"uid": uidIndexer[*nvapi.ComputeDomain],
	})
	if err != nil {
		return fmt.Errorf("error adding indexer for ComputeDomain UID: %w", err)
	}

	_, err = m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			m.config.workQueue.Enqueue(obj, m.onAddOrUpdate)
		},
		UpdateFunc: func(objOld, objNew any) {
			m.config.workQueue.Enqueue(objNew, m.onAddOrUpdate)
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

	return nil
}

// Stop stops the compute domain manager.
func (m *ComputeDomainManager) Stop() error {
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

// Get gets the ComputeDomain by UID from the informer cache.
func (m *ComputeDomainManager) Get(uid string) (*nvapi.ComputeDomain, error) {
	objs, err := m.informer.GetIndexer().ByIndex("uid", uid)
	if err != nil {
		return nil, fmt.Errorf("error retrieving ComputeDomain by UID: %w", err)
	}
	if len(objs) == 0 {
		return nil, nil
	}
	if len(objs) != 1 {
		return nil, fmt.Errorf("multiple ComputeDomains with the same UID")
	}
	cd, ok := objs[0].(*nvapi.ComputeDomain)
	if !ok {
		return nil, fmt.Errorf("error casting to ComputeDomain")
	}
	return cd, nil
}

// onAddOrUpdate handles the addition or update of a ComputeDomain.
func (m *ComputeDomainManager) onAddOrUpdate(ctx context.Context, obj any) error {
	// Cast the object to a ComputeDomain object
	o, ok := obj.(*nvapi.ComputeDomain)
	if !ok {
		return fmt.Errorf("failed to cast to ComputeDomain")
	}

	// Get the latest ComputeDomain object from the informer cache since we
	// plan to update it later and always *must* have the latest version.
	cd, err := m.Get(string(o.GetUID()))
	if err != nil {
		return fmt.Errorf("error getting latest ComputeDomain: %w", err)
	}
	if cd == nil {
		return nil
	}

	// Skip ComputeDomains that don't match on UUID
	if string(cd.UID) != m.config.computeDomainUUID {
		klog.Errorf("ComputeDomain processed with non-matching UID (%v, %v)", cd.UID, m.config.computeDomainUUID)
		return nil
	}

	// Update node info in ComputeDomain
	if err := m.UpdateComputeDomainNodeInfo(ctx, cd); err != nil {
		return fmt.Errorf("error updating node info in ComputeDomain: %w", err)
	}

	return nil
}

// UpdateComputeDomainNodeInfo updates the Nodes field in the ComputeDomain
// with info about the ComputeDomain daemon running on this node.
func (m *ComputeDomainManager) UpdateComputeDomainNodeInfo(ctx context.Context, cd *nvapi.ComputeDomain) error {
	var nodeInfo *nvapi.ComputeDomainNode

	// Create a deep copy of the ComputeDomain to avoid modifying the original
	newCD := cd.DeepCopy()

	defer func() {
		m.MaybePushNodesUpdate(newCD)
	}()

	// Try to find an existing entry for the current k8s node
	for _, node := range newCD.Status.Nodes {
		if node.Name == m.config.nodeName {
			nodeInfo = node
			break
		}
	}

	// If there is one and its IP is the same as this one, we are done
	if nodeInfo != nil && nodeInfo.IPAddress == m.config.podIP {
		return nil
	}

	// If there isn't one, create one and append it to the list
	if nodeInfo == nil {
		nodeInfo = &nvapi.ComputeDomainNode{
			Name:     m.config.nodeName,
			CliqueID: m.config.cliqueID,
		}
		newCD.Status.Nodes = append(newCD.Status.Nodes, nodeInfo)
	}

	// Unconditionally update its IP address. Note that the nodeInfo.IPAddress
	// as of now translates into a pod IP address and may therefore change
	// across pod restarts.
	nodeInfo.IPAddress = m.config.podIP

	// Conditionally update its status
	if newCD.Status.Status == "" {
		newCD.Status.Status = nvapi.ComputeDomainStatusNotReady
	}

	// Update the status
	if _, err := m.config.clientsets.Nvidia.ResourceV1beta1().ComputeDomains(newCD.Namespace).UpdateStatus(ctx, newCD, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("error updating nodes in ComputeDomain status: %w", err)
	}

	return nil
}

// If we've reached the expected number of nodes and if there was actually a
// change compared to the previously known set of nodes: pass info to IMEX
// daemon controller.
func (m *ComputeDomainManager) MaybePushNodesUpdate(cd *nvapi.ComputeDomain) {
	if len(cd.Status.Nodes) != cd.Spec.NumNodes {
		klog.Infof("numNodes: %d, nodes seen: %d", cd.Spec.NumNodes, len(cd.Status.Nodes))
		return
	}

	newIPs := getIPSet(cd.Status.Nodes)
	previousIPs := getIPSet(m.previousNodes)

	// Compare sets (i.e., without paying attention to order). Note: the order
	// of IP addresses written to the IMEX daemon's config file might matter (in
	// the sense that if across config files the set is equal but the order is
	// not: that may lead to an IMEX daemon startup error). Maybe we should
	// perform a stable sort of IP addresses before writing them to the nodes
	// config file.
	if !maps.Equal(newIPs, previousIPs) {
		klog.Infof("IP set changed: previous: %v; new: %v", previousIPs, newIPs)
		m.previousNodes = cd.Status.Nodes
		m.updatedNodesChan <- cd.Status.Nodes
	} else {
		klog.Infof("IP set did not change")
	}
}

func (m *ComputeDomainManager) GetNodesUpdateChan() chan []*nvapi.ComputeDomainNode {
	// Yields numNodes-size nodes updates.
	return m.updatedNodesChan
}

func getIPSet(nodeInfos []*nvapi.ComputeDomainNode) IPSet {
	set := make(IPSet)
	for _, n := range nodeInfos {
		set[n.IPAddress] = struct{}{}
	}
	return set
}
