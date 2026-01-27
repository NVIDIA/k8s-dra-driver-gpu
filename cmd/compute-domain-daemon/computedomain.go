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
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"sort"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
)

const (
	// Detecting when a CD daemon transitions from NotReady to Ready (based on
	// the startup probe) at the moment sometimes requires an informer resync,
	// see https://github.com/NVIDIA/k8s-dra-driver-gpu/issues/742.
	informerResyncPeriod = 4 * time.Minute
	mutationCacheTTL     = time.Hour
)

// GetComputeDomainFunc is a function type for getting a ComputeDomain by UID.
type GetComputeDomainFunc func() (*nvapi.ComputeDomain, error)

type IPSet map[string]struct{}

// ComputeDomainManager watches compute domains and updates their status with
// info about the ComputeDomain daemon running on this node.
type ComputeDomainManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	// factory  nvinformers.SharedInformerFactory
	// informer cache.SharedIndexInformer

	// Note: if `previousNodes` is empty it means we're early in the daemon's
	// lifecycle and the IMEX daemon child process wasn't started yet.
	previousNodes    []*nvapi.ComputeDomainNode
	updatedNodesChan chan []*nvapi.ComputeDomainNode

	podManager     *PodManager
	latestCD       *nvapi.ComputeDomain
	latestCDupdate time.Time
	//mutationCache cache.MutationCache
}

// NewComputeDomainManager creates a new ComputeDomainManager instance.
func NewComputeDomainManager(config *ManagerConfig) *ComputeDomainManager {
	// factory := nvinformers.NewSharedInformerFactoryWithOptions(
	// 	config.clientsets.Nvidia,
	// 	informerResyncPeriod,
	// 	nvinformers.WithNamespace(config.computeDomainNamespace),
	// 	nvinformers.WithTweakListOptions(func(opts *metav1.ListOptions) {
	// 		opts.FieldSelector = fmt.Sprintf("metadata.name=%s", config.computeDomainName)
	// 		// Forces the initial List to be served from the API Server's memory (Watch Cache)
	// 		// rather than making a "Quorum Read" to etcd.
	// 		opts.ResourceVersion = "0"
	// 	}),
	// )
	// informer := factory.Resource().V1beta1().ComputeDomains().Informer()

	m := &ComputeDomainManager{
		config: config,
		// factory:          factory,
		// informer:         informer,
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

	// For large CDs, smear the first contact to the API server slightly out
	// over time.
	// startupJitter := time.Duration(rand.Intn(35000)) * time.Millisecond
	// klog.V(6).Infof("Delay startup by %s ms", startupJitter)
	// time.Sleep(startupJitter)

	// err := m.informer.AddIndexers(cache.Indexers{
	// 	"uid": uidIndexer[*nvapi.ComputeDomain],
	// })
	// if err != nil {
	// 	return fmt.Errorf("error adding indexer for ComputeDomain UID: %w", err)
	// }

	// Create mutation cache to track our own updates
	// m.mutationCache = cache.NewIntegerResourceVersionMutationCache(
	// 	klog.Background(),
	// 	m.informer.GetStore(),
	// 	m.informer.GetIndexer(),
	// 	mutationCacheTTL,
	// 	true,
	// )

	m.podManager = NewPodManager(m.config, m.Get)

	// Use `WithKey` with hard-coded key, to cancel any previous update task (we
	// want to make sure that the latest CD status update wins).
	// _, err = m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
	// 	AddFunc: func(obj any) {
	// 		m.config.workQueue.EnqueueWithKey(obj, "cd", m.onAddOrUpdate)
	// 	},
	// 	UpdateFunc: func(objOld, objNew any) {
	// 		m.config.workQueue.EnqueueWithKey(objNew, "cd", m.onAddOrUpdate)
	// 	},
	// })
	// if err != nil {
	// 	return fmt.Errorf("error adding event handlers for ComputeDomain informer: %w", err)
	// }

	// m.waitGroup.Add(1)
	// go func() {
	// 	defer m.waitGroup.Done()
	// 	m.factory.Start(ctx.Done())
	// }()

	// if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
	// 	return fmt.Errorf("informer cache sync for ComputeDomains failed")
	// }

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.pollCD(ctx)
	}()

	if err := m.podManager.Start(ctx); err != nil {
		return fmt.Errorf("failed to start pod manager: %w", err)
	}

	return nil
}

func (m *ComputeDomainManager) pollCD(ctx context.Context) {
	// Polling loop startup jitter.
	delay := time.Duration(rand.Intn(1500)) * time.Millisecond
	timeoutErrors := 0
	for {
		klog.V(6).Infof("next CD GET in %s", delay)
		time.Sleep(delay)

		// Note: I should be the only one in this entire process that issues
		// these GET requests. ResourceVersion: "0" means: use API server cache
		// (not 'qorum read').
		//opts := metav1.GetOptions{ResourceVersion: "0"}
		opts := metav1.GetOptions{}
		t0 := time.Now()
		cd, err := m.config.clientsets.Nvidia.ResourceV1beta1().ComputeDomains(m.config.computeDomainNamespace).Get(ctx, m.config.computeDomainName, opts)
		if err != nil {
			klog.Infof("pollCD: failed to GET: %s", err)
			delay = 10 * time.Second
			continue
		}
		// t_get_cd may be large-ish, as in 3.825 s
		getLatencySeconds := time.Since(t0).Seconds()
		klog.V(6).Infof("t_get_cd %.3f s", getLatencySeconds)

		// if time.Since(m.latestCDupdate).Seconds() < 1 {
		// 	time.Sleep(2 * time.Second)
		// 	continue
		// }

		// If we have processed this before: abort iteration.
		if m.latestCD != nil && cd.GetResourceVersion() == m.latestCD.GetResourceVersion() {
			klog.V(6).Infof("pollCD: seen this CD version before")
			delay = 15 * time.Second
			continue
		}

		// If the last GET was slow, let's wait a bit with our PATCHing.
		// time.Sleep(time.Duration(int(getLatencySeconds/1000.0)*3) * time.Millisecond)
		cd, err = m.EnsureNodeInfoInCD(ctx, cd.DeepCopy())
		if err != nil && errors.Is(err, context.DeadlineExceeded) {
			timeoutErrors += 1
			//backoffMs := 2000*timeoutErrors + rand.Intn(1000*timeoutErrors)
			//backoffMs = int(math.Max(float64(backoffMs), float64(6000)))
			//backoff := time.Duration(backoffMs) * time.Millisecond

			rndDelay := time.Duration(10000+rand.Intn(35000)) * time.Millisecond
			klog.V(6).Infof("EnsureNodeInfoInCD: PATCH failed with timeout (%d), delay next poll by %s", timeoutErrors, rndDelay)
			delay = rndDelay
			continue
		}

		timeoutErrors = 0

		if err != nil {
			klog.Infof("pollCD: EnsureNodeInfoInCD failed: %s", err)
			delay = 15 * time.Second
			continue
		}

		// Start and/or update IMEX daemon.
		m.MaybePushNodesUpdate(cd)

		// Expose this latest CD object to other consumers, and take note
		// that we have processed this successfully.
		m.trackLatest(cd)

		// Temporarily increase poll period if the last API server get was slow.
		//time.Sleep(time.Duration(int(math.Min(getLatencySeconds/1000.0, 7000.0))) * time.Millisecond)
		delay = 10 * time.Second
	}
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

func (m *ComputeDomainManager) Get() (*nvapi.ComputeDomain, error) {
	if m.latestCD == nil {
		return nil, fmt.Errorf("node entry not yet inserted")
	}
	return m.latestCD, nil
	// cds, err := getByComputeDomainUID[*nvapi.ComputeDomain](m.mutationCache, uid)
	// if err != nil {
	// 	return nil, fmt.Errorf("error retrieving ComputeDomain by UID: %w", err)
	// }
	// if len(cds) == 0 {
	// 	return nil, nil
	// }
	// if len(cds) != 1 {
	// 	return nil, fmt.Errorf("multiple ComputeDomains with the same UID")
	// }
	// return cds[0], nil
}

// onAddOrUpdate handles the addition or update of a ComputeDomain. Here, we
// receive updates not for all CDs in the system, but only for the CD that we
// are registered for (filtered by CD name). Note that the informer triggers
// this callback once upon startup for all existing objects.
// func (m *ComputeDomainManager) onAddOrUpdate(ctx context.Context, obj any) error {
// 	// Cast the object to a ComputeDomain object
// 	o, ok := obj.(*nvapi.ComputeDomain)
// 	if !ok {
// 		return fmt.Errorf("failed to cast to ComputeDomain")
// 	}

// 	// Get the latest ComputeDomain object from the mutation cache (backed by
// 	// the informer cache) since we plan to update it later and always *must*
// 	// have the latest version.
// 	cd, err := m.Get(string(o.GetUID()))
// 	if err != nil {
// 		return fmt.Errorf("error getting latest ComputeDomain: %w", err)
// 	}
// 	if cd == nil {
// 		return nil
// 	}

// 	// Because the informer only filters by name:
// 	// Skip ComputeDomains that don't match on UUID
// 	if string(cd.UID) != m.config.computeDomainUUID {
// 		klog.Warningf("ComputeDomain processed with non-matching UID (%v, %v)", cd.UID, m.config.computeDomainUUID)
// 		return nil
// 	}

// 	// Update node info in ComputeDomain, if required.
// 	cd, err = m.EnsureNodeInfoInCD(ctx, cd)
// 	if err != nil {
// 		return fmt.Errorf("CD update: failed to insert/update node info in CD: %w", err)
// 	}
// 	m.MaybePushNodesUpdate(cd)

// 	return nil
// }

// EnsureNodeInfoInCD makes sure that the current node (by node name) is
// represented in the `Nodes` field in the ComputeDomain object, and that it
// reports the IP address of this current pod running the CD daemon. If mutation
// is needed (first insertion, or IP address update) and successful, it reflects
// the mutation in `m.mutationCache`.
// TODO: rename function?
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
		// Get an available index for this new node (local point of view, API
		// server may tell us later that this index was chosen poorly).
		nextIndex, err := getRandomAvailableIndex(m.config.cliqueID, newCD.Status.Nodes, m.config.maxNodesPerIMEXDomain)
		if err != nil {
			return nil, fmt.Errorf("error getting next available index: %w", err)
		}

		myNode = &nvapi.ComputeDomainNode{
			Name:     m.config.nodeName,
			CliqueID: m.config.cliqueID,
			Index:    nextIndex,
			// This is going to be switched to Ready by podmanager.
			Status: nvapi.ComputeDomainStatusNotReady,
		}

		klog.Infof("CD status does not contain node name '%s' yet, try to insert myself: %v", m.config.nodeName, myNode)
	}

	// Unconditionally update its IP address. Note that the nodeInfo.IPAddress
	// as of now translates into a pod IP address and may therefore changes
	// across pod restarts.
	myNode.IPAddress = m.config.podIP

	// Detect and handle DNS index collision where my self-chosen DNS index
	// appears elsewhere (among the nodes with the same cliqueID). If
	// `m.previousNodes` is empty, we haven't started the IMEX daemon yet and
	// hence can change our previous choice safely.
	if len(m.previousNodes) == 0 {
		for _, other := range newCD.Status.Nodes {
			// Skip items in different cliques, and also myself.
			if other.CliqueID != m.config.cliqueID {
				continue
			}
			if other.Name == m.config.nodeName {
				continue
			}
			if other.Index == myNode.Index {
				idx, err := getRandomAvailableIndex(m.config.cliqueID, newCD.Status.Nodes, m.config.maxNodesPerIMEXDomain)
				if err != nil {
					return nil, fmt.Errorf("error getting next available index: %w", err)
				}
				myNode.Index = idx
				klog.V(4).Infof("EnsureNodeInfoInCD: IMEX daemon not started yet, DNS index collision with %v, picked new index: %d", other, idx)
				jitter := time.Duration(rand.Intn(500)) * time.Millisecond
				//klog.V(6).Infof("Delay startup by %s", startupJitter)
				time.Sleep(jitter)
			}
		}
	}

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

	// Before proceeding to send PATCH, add a tiny amount of jitter
	// (for large N).
	// jitter := time.Duration(rand.Intn(2000)) * time.Millisecond
	// time.Sleep(jitter)
	updatedCD, err := m.patchCD(ctx, patchBytes)
	if err != nil {
		// if err == context.DeadlineExceeded {
		// 	jitter := time.Duration(rand.Intn(5000)) * time.Millisecond
		// 	klog.V(6).Infof("EnsureNodeInfoInCD: PATCH failed with timeout, sleep %d ms", jitter)
		// 	time.Sleep(jitter)
		// }
		return nil, fmt.Errorf("error patching ComputeDomain status: %w", err)
	}
	//m.trackLatest(updatedCD)
	// m.mutationCache.Mutation(updatedCD)

	klog.Infof("Successfully inserted/updated node in CD (nodeinfo: %v)", myNode)
	return updatedCD, nil
}

// The Index field in the Nodes section of the ComputeDomain status ensures a
// consistent IP-to-DNS name mapping across all machines within a given IMEX
// domain. Each node's index directly determines its DNS name using the format
// "compute-domain-daemon-{index}".
//
// getNextAvailableIndex finds the next available index for the current node by
// seeing which ones are already taken by other nodes in the ComputeDomain
// status that have the same cliqueID. It fills in gaps where it can, and returns
// an error if no index is available within maxNodesPerIMEXDomain.
//
// By filling gaps in the index sequence (rather than always appending), we
// maintain stable DNS names for existing nodes even when intermediate nodes
// are removed from the compute domain and new ones are added.
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

	// Skip `maxNodesPerIMEXDomain` check in the special case of no clique ID
	// being set: this means that this node does not actually run an IMEX daemon
	// managed by us and the set of nodes in this "noop" mode in this CD is
	// allowed to grow larger than maxNodesPerIMEXDomain.
	if currentCliqueID == "" {
		return nextIndex, nil
	}

	// Ensure nextIndex is within the range 0..maxNodesPerIMEXDomain
	if nextIndex < 0 || nextIndex >= maxNodesPerIMEXDomain {
		return -1, fmt.Errorf("no available indices within maxNodesPerIMEXDomain (%d) for cliqueID %s", maxNodesPerIMEXDomain, currentCliqueID)
	}

	return nextIndex, nil
}

func getRandomAvailableIndex(currentCliqueID string, nodes []*nvapi.ComputeDomainNode, maxNodesPerIMEXDomain int) (int, error) {
	// Determine upper bound based on the clique ID: if no clique ID is set
	// ("noop" mode) use big limit. Otherwise, use standard domain limit.
	max := maxNodesPerIMEXDomain
	if currentCliqueID == "" {
		max = 10000
	}

	// Only consider nodes with the same cliqueID.
	var cliqueNodes []*nvapi.ComputeDomainNode
	for _, node := range nodes {
		if node.CliqueID == currentCliqueID {
			cliqueNodes = append(cliqueNodes, node)
		}
	}

	// Collect used indices.
	used := make(map[int]bool)
	for _, node := range cliqueNodes {
		used[node.Index] = true
	}

	// Build set of free indices, considering upper bound `max`. Allocating a
	// slice even for 10k ints is efficient in Go, i.e. we can safely build the
	// full candidate list.
	candidates := make([]int, 0, max)
	for i := 0; i < max; i++ {
		if !used[i] {
			candidates = append(candidates, i)
		}
	}

	// Make random selection from candidate list.
	if len(candidates) > 0 {
		return candidates[rand.Intn(len(candidates))], nil
	}

	return -1, fmt.Errorf("no available indices within limit (%d) for cliqueID '%s'", max, currentCliqueID)
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

	// Do not update the IMEX daemon config if the current nodes list
	// contains any duplicate DNS index (within our clique).
	if m.HasDuplicateIndex(cd.Status.Nodes, m.config.cliqueID) {
		return
	}

	newIPs := getIPSetForClique(cd.Status.Nodes, m.config.cliqueID)
	previousIPs := getIPSetForClique(m.previousNodes, m.config.cliqueID)
	added, removed := previousIPs.Diff(newIPs)

	// Compare sets (i.e., without paying attention to order). Note: the order
	// of IP addresses written to the IMEX daemon's config file might matter (in
	// the sense that if across config files the set is equal but the order is
	// not: that may lead to an IMEX daemon startup error). Maybe we should
	// perform a stable sort of IP addresses before writing them to the nodes
	// config file. Note/TODO: we probably want to limit this check to IP
	// addresses relevant to _this_ clique.
	if !maps.Equal(newIPs, previousIPs) {
		klog.V(2).Infof("IP set for clique changed.\nAdded:%v\nRemoved:%v", added, removed)
		m.previousNodes = cd.Status.Nodes
		m.updatedNodesChan <- cd.Status.Nodes
	} else {
		klog.V(6).Infof("IP set for clique did not change")
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
	_, err = m.patchCD(ctx, patchBytes)
	if err != nil {
		return fmt.Errorf("error patching ComputeDomain status for node removal: %w", err)
	}
	//m.trackLatest(updatedCD)
	//m.mutationCache.Mutation(updatedCD)

	klog.Infof("Successfully removed node with IP %s from ComputeDomain %s/%s", m.config.podIP, m.config.computeDomainNamespace, m.config.computeDomainName)
	return nil
}

func (m *ComputeDomainManager) trackLatest(cd *nvapi.ComputeDomain) {
	// here we could implement a resource version comparator. for now, trust the
	// code.
	m.latestCD = cd
	m.latestCDupdate = time.Now()
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
	// Patching may result in contention indicated by the response not coming
	// back in time. As a pragmatic of backpressure, abort early and let the
	// caller wait.
	childCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	klog.V(6).Infof("try to PATCH CD")
	updatedCD, err := m.config.clientsets.Nvidia.ResourceV1beta1().ComputeDomains(m.config.computeDomainNamespace).Patch(
		childCtx,
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

func getIPSetForClique(nodeInfos []*nvapi.ComputeDomainNode, cliqueID string) IPSet {
	set := make(IPSet)
	for _, n := range nodeInfos {
		if n.CliqueID == cliqueID {
			set[n.IPAddress] = struct{}{}
		}
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

// HasDuplicateIndex iterates over the list of ComputeDomainNodes (in this CD,
// and in this clique), and returns true if any Index appears more than once.
func (m *ComputeDomainManager) HasDuplicateIndex(nodeInfos []*nvapi.ComputeDomainNode, cliqueID string) bool {
	seen := make(map[int]struct{})

	for _, node := range nodeInfos {
		// Ignore nodes in a different clique.
		if node.CliqueID != cliqueID {
			continue
		}

		if _, exists := seen[node.Index]; exists {
			klog.V(6).Infof("DNS index collision detected: %v uses an index seen before (we are node %v)", node, m.config.nodeName)
			return true
		}

		// Mark as seen.
		seen[node.Index] = struct{}{}
	}

	return false
}

// Diff compares two IP sets. It returns a list of IPs that were added and a
// list of IPs that were removed.
func (s IPSet) Diff(cmp IPSet) ([]string, []string) {
	var added []string
	var removed []string

	// Check for IPs in s (reference) that are NOT in cmp (removed)
	for ip := range s {
		if _, exists := cmp[ip]; !exists {
			removed = append(removed, ip)
		}
	}

	// Check for IPs in cmp that are NOT in reference s (added)
	for ip := range cmp {
		if _, exists := s[ip]; !exists {
			added = append(added, ip)
		}
	}

	sort.Strings(added)
	sort.Strings(removed)

	return added, removed
}
