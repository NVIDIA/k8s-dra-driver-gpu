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
	"encoding/json"
	"fmt"
	"maps"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
	nvinformers "github.com/NVIDIA/k8s-dra-driver-gpu/pkg/nvidia.com/informers/externalversions"
)

const (
	// Detecting when a CD daemon transitions from NotReady to Ready (based on
	// the startup probe) at the moment sometimes requires an informer resync,
	// see https://github.com/NVIDIA/k8s-dra-driver-gpu/issues/742.
	informerResyncPeriod = 4 * time.Minute
	mutationCacheTTL     = time.Hour
)

// GetComputeDomainFunc is a function type for getting a ComputeDomain by UID.
type GetComputeDomainFunc func(uid string) (*nvapi.ComputeDomain, error)

type IPSet map[string]struct{}

// ComputeDomainManager watches compute domains and updates their status with
// info about the ComputeDomain daemon running on this node.
type ComputeDomainManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  nvinformers.SharedInformerFactory
	informer cache.SharedIndexInformer

	// Note: if `previousNodes` is empty it means we're early in the daemon's
	// lifecycle and the IMEX daemon child process wasn't started yet.
	previousNodes    []*nvapi.ComputeDomainNode
	updatedNodesChan chan []*nvapi.ComputeDomainNode

	podManager    *PodManager
	mutationCache cache.MutationCache
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

	// Create mutation cache to track our own updates
	m.mutationCache = cache.NewIntegerResourceVersionMutationCache(
		klog.Background(),
		m.informer.GetStore(),
		m.informer.GetIndexer(),
		mutationCacheTTL,
		true,
	)

	m.podManager = NewPodManager(m.config, m.Get, m.mutationCache)

	// Use `WithKey` with hard-coded key, to cancel any previous update task (we
	// want to make sure that the latest CD status update wins).
	_, err = m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			m.config.workQueue.EnqueueWithKey(obj, "cd", m.onAddOrUpdate)
		},
		UpdateFunc: func(objOld, objNew any) {
			m.config.workQueue.EnqueueWithKey(objNew, "cd", m.onAddOrUpdate)
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

	if err := m.podManager.Start(ctx); err != nil {
		return fmt.Errorf("failed to start pod manager: %w", err)
	}

	return nil
}

// Stop stops the compute domain manager.
//
//nolint:contextcheck
func (m *ComputeDomainManager) Stop() error {
	// Stop the pod manager first
	if err := m.podManager.Stop(); err != nil {
		klog.Errorf("Failed to stop pod manager: %v", err)
	}

	// Create a new context for cleanup operations since the original context might be cancelled
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cleanupCancel()

	// Attempt to remove this node from the ComputeDomain status before shutting down
	// Don't return error here as we still want to proceed with shutdown
	if err := m.removeNodeFromComputeDomain(cleanupCtx); err != nil {
		klog.Errorf("Failed to remove node from ComputeDomain during shutdown: %v", err)
	}

	if m.cancelContext != nil {
		m.cancelContext()
	}

	m.waitGroup.Wait()
	return nil
}

// Get gets the ComputeDomain by UID from the mutation cache.
func (m *ComputeDomainManager) Get(uid string) (*nvapi.ComputeDomain, error) {
	cds, err := getByComputeDomainUID[*nvapi.ComputeDomain](m.mutationCache, uid)
	if err != nil {
		return nil, fmt.Errorf("error retrieving ComputeDomain by UID: %w", err)
	}
	if len(cds) == 0 {
		return nil, nil
	}
	if len(cds) != 1 {
		return nil, fmt.Errorf("multiple ComputeDomains with the same UID")
	}
	return cds[0], nil
}

// onAddOrUpdate handles the addition or update of a ComputeDomain. Here, we
// receive updates not for all CDs in the system, but only for the CD that we
// are registered for (filtered by CD name). Note that the informer triggers
// this callback once upon startup for all existing objects.
func (m *ComputeDomainManager) onAddOrUpdate(ctx context.Context, obj any) error {
	// Cast the object to a ComputeDomain object
	o, ok := obj.(*nvapi.ComputeDomain)
	if !ok {
		return fmt.Errorf("failed to cast to ComputeDomain")
	}

	// Get the latest ComputeDomain object from the mutation cache (backed by
	// the informer cache) since we plan to update it later and always *must*
	// have the latest version.
	cd, err := m.Get(string(o.GetUID()))
	if err != nil {
		return fmt.Errorf("error getting latest ComputeDomain: %w", err)
	}
	if cd == nil {
		return nil
	}

	// Because the informer only filters by name:
	// Skip ComputeDomains that don't match on UUID
	if string(cd.UID) != m.config.computeDomainUUID {
		klog.Warningf("ComputeDomain processed with non-matching UID (%v, %v)", cd.UID, m.config.computeDomainUUID)
		return nil
	}

	// Update node info in ComputeDomain, if required.
	cd, err = m.EnsureNodeInfoInCD(ctx, cd)
	if err != nil {
		return fmt.Errorf("CD update: failed to insert/update node info in CD: %w", err)
	}
	m.MaybePushNodesUpdate(cd)

	return nil
}

// EnsureNodeInfoInCD makes sure that the current node (by node name) is
// represented in the `Nodes` field in the ComputeDomain object, and that it
// reports the IP address of this current pod running the CD daemon. If mutation
// is needed (first insertion, or IP address update) and successful, it reflects
// the mutation in `m.mutationCache`.
func (m *ComputeDomainManager) EnsureNodeInfoInCD(ctx context.Context, cd *nvapi.ComputeDomain) (*nvapi.ComputeDomain, error) {
	var myNode, myNodePrevious *nvapi.ComputeDomainNode

	// Create a deep copy of the ComputeDomain to avoid modifying the original
	newCD := cd.DeepCopy()

	// Try to find an existing entry for the current k8s node
	for _, node := range newCD.Status.Nodes {
		if node.Name == m.config.nodeName {
			myNode = node.DeepCopy()
			myNodePrevious = node.DeepCopy()
			break
		}
	}

	// Create new ComputeDomainNode object representing myself, and insert it into the nodes list.
	if myNode == nil {
		myNode = &nvapi.ComputeDomainNode{
			Name:     m.config.nodeName,
			CliqueID: m.config.cliqueID,
			Index:    m.config.cliqueNodeIndex,
			// This is going to be switched to Ready by podmanager.
			Status: nvapi.ComputeDomainStatusNotReady,
		}

		klog.Infof("CD status does not contain node name '%s' yet, try to insert myself: %v", m.config.nodeName, myNode)
	}

	// Unconditionally update its IP address. Note that the nodeInfo.IPAddress
	// as of now translates into a pod IP address and may therefore changes
	// across pod restarts.
	myNode.IPAddress = m.config.podIP

	if myNodePrevious != nil && *myNodePrevious == *myNode {
		klog.V(6).Infof("EnsureNodeInfoInCD noop: no change (%v)", *myNode)
		return newCD, nil
	}

	// Use server-side apply (SSA) to perform insertion or update, with a
	// localized patch affecting just one `ComputeDomainNode` item in the
	// `status.nodes` list. See
	// https://github.com/NVIDIA/k8s-dra-driver-gpu/issues/821 for context.
	patchBytes, err := generatePatchForNodeInfo([]*nvapi.ComputeDomainNode{myNode})
	if err != nil {
		return nil, fmt.Errorf("could not serialize patch: %w", err)
	}

	updatedCD, err := m.patchCD(ctx, patchBytes)
	if err != nil {
		return nil, fmt.Errorf("error patching ComputeDomain status: %w", err)
	}
	m.mutationCache.Mutation(updatedCD)

	klog.Infof("Successfully inserted/updated node in CD (nodeinfo: %v)", myNode)
	return updatedCD, nil
}

// If there was actually a change compared to the previously known set of
// nodes: pass info to IMEX daemon controller.
func (m *ComputeDomainManager) MaybePushNodesUpdate(cd *nvapi.ComputeDomain) {
	// When not running with the 'IMEXDaemonsWithDNSNames' feature enabled,
	// wait for all 'numNodes' nodes to show up before sending an update.
	if !featuregates.Enabled(featuregates.IMEXDaemonsWithDNSNames) {
		if len(cd.Status.Nodes) != cd.Spec.NumNodes {
			klog.Infof("numNodes: %d, nodes seen: %d", cd.Spec.NumNodes, len(cd.Status.Nodes))
			return
		}
	}

	newIPs := getIPSet(cd.Status.Nodes)
	previousIPs := getIPSet(m.previousNodes)

	// Compare sets (i.e., without paying attention to order). Note: the order
	// of IP addresses written to the IMEX daemon's config file might matter (in
	// the sense that if across config files the set is equal but the order is
	// not: that may lead to an IMEX daemon startup error). Maybe we should
	// perform a stable sort of IP addresses before writing them to the nodes
	// config file. Note/TODO: we probably want to limit this check to IP
	// addresses relevant to _this_ clique.
	if !maps.Equal(newIPs, previousIPs) {
		klog.V(2).Infof("IP set changed")
		// This log message gets large for large node numbers
		klog.V(6).Infof("previous: %v; new: %v", previousIPs, newIPs)
		m.previousNodes = cd.Status.Nodes
		m.updatedNodesChan <- cd.Status.Nodes
	} else {
		klog.V(6).Infof("IP set did not change")
	}
}

func (m *ComputeDomainManager) GetNodesUpdateChan() chan []*nvapi.ComputeDomainNode {
	// Yields numNodes-size nodes updates.
	return m.updatedNodesChan
}

// removeNodeFromComputeDomain removes the current node's entry from the ComputeDomain status.
func (m *ComputeDomainManager) removeNodeFromComputeDomain(ctx context.Context) error {
	// Patch with an empty object: instructs to delete all list items owned by
	// the SSA field manager (which is precisely one:the item for this node, as
	// identified by node name).
	patchBytes, err := generatePatchForNodeInfo([]*nvapi.ComputeDomainNode{})
	if err != nil {
		return fmt.Errorf("could not serialize patch: %w", err)
	}
	updatedCD, err := m.patchCD(ctx, patchBytes)
	if err != nil {
		return fmt.Errorf("error patching ComputeDomain status for node removal: %w", err)
	}
	m.mutationCache.Mutation(updatedCD)

	klog.Infof("Successfully removed node with IP %s from ComputeDomain %s/%s", m.config.podIP, m.config.computeDomainNamespace, m.config.computeDomainName)
	return nil
}

// Notes:
//
// 1) Field owner and field manager are referring to the same concept.
// Specifically, `client.FieldOwner("foo")` (which is often shown in
// documentation snippets) renders as `PatchOptions{FieldManager: "foo"}`.
//
// 2) In SSA documentation, one finds the `force: true` concept -- it can be
// used to take ownership of fields that are currently owned by a different
// field manager. This is not needed in our context.
//
// 3) We cannot get away with one shared owner/manager that is used across
// nodes. SSA tracks field ownership at the field manager level, not at the
// client/process level. When a field manager applies a patch with a list, it's
// declaring the complete desired state for all entries that this field manager
// owns. When multiple writers use the same field manager name, all such writes
// are treated as coming from one logical actor. Notably, each apply operation
// must include all fields that field manager owns. If node A adds/updates entry
// "node-a" and node B adds/updates entry "node-b" using the same field manager,
// SSA interprets the omission as intent to delete those entries.
//
// Note:
//   - The `Patch()` method requires the patch itself to be provided as
//     byte sequence (as JSON document).
//   - The `apiVersion` and `kind“ fields are required in the patch payload.
//   - We verified that the API server object returned in response to a PATCH
//     request reflects both, the patch, and also patches made by other
//     owners if they happened in the meantime.
func (m *ComputeDomainManager) patchCD(ctx context.Context, patch []byte) (*nvapi.ComputeDomain, error) {
	updatedCD, err := m.config.clientsets.Nvidia.ResourceV1beta1().ComputeDomains(m.config.computeDomainNamespace).Patch(
		ctx,
		m.config.computeDomainName,
		types.ApplyPatchType,
		patch,
		metav1.PatchOptions{
			FieldManager: fmt.Sprintf("cd-writer-%s", m.config.nodeName),
		},
		"status",
	)
	return updatedCD, err
}

func getIPSet(nodeInfos []*nvapi.ComputeDomainNode) IPSet {
	set := make(IPSet)
	for _, n := range nodeInfos {
		set[n.IPAddress] = struct{}{}
	}
	return set
}

func generatePatchForNodeInfo(nodes []*nvapi.ComputeDomainNode) ([]byte, error) {
	patch := map[string]interface{}{
		"apiVersion": "resource.nvidia.com/v1beta1",
		"kind":       "ComputeDomain",
		"status": map[string]interface{}{
			"nodes": nodes,
		},
	}
	patchBytes, err := json.Marshal(patch)
	return patchBytes, err
}
