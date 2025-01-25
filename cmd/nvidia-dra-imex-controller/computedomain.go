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

	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver/api/nvidia.com/resource/v1beta1"
	nvinformers "github.com/NVIDIA/k8s-dra-driver/pkg/nvidia.com/informers/externalversions"
	nvlisters "github.com/NVIDIA/k8s-dra-driver/pkg/nvidia.com/listers/resource/v1beta1"
)

type GetComputeDomainFunc func(uid string) (*nvapi.ComputeDomain, error)

const (
	informerResyncPeriod = 10 * time.Minute

	computeDomainLabelKey  = "resource.nvidia.com/computeDomain"
	computeDomainFinalizer = computeDomainLabelKey
)

type ComputeDomainManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  nvinformers.SharedInformerFactory
	informer cache.SharedIndexInformer
	lister   nvlisters.ComputeDomainLister

	deploymentManager    *DeploymentManager
	deviceClassManager   *DeviceClassManager
	resourceClaimManager *ResourceClaimManager
}

// NewComputeDomainManager creates a new ComputeDomainManager.
func NewComputeDomainManager(config *ManagerConfig) *ComputeDomainManager {
	factory := nvinformers.NewSharedInformerFactory(config.clientsets.Nvidia, informerResyncPeriod)
	informer := factory.Resource().V1beta1().ComputeDomains().Informer()
	lister := nvlisters.NewComputeDomainLister(informer.GetIndexer())

	m := &ComputeDomainManager{
		config:   config,
		factory:  factory,
		informer: informer,
		lister:   lister,
	}
	m.deploymentManager = NewDeploymentManager(config, m.Get)
	m.deviceClassManager = NewDeviceClassManager(config, m.Get)
	m.resourceClaimManager = NewResourceClaimManager(config, m.Get)

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
			m.config.workQueue.Enqueue(obj, m.onComputeDomainAdd)
		},
		DeleteFunc: func(obj any) {
			m.config.workQueue.Enqueue(obj, m.onComputeDomainDelete)
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

	if err := m.deploymentManager.Start(ctx); err != nil {
		return fmt.Errorf("error starting Deployment manager: %w", err)
	}

	if err := m.deviceClassManager.Start(ctx); err != nil {
		return fmt.Errorf("error creating DeviceClass manager: %w", err)
	}

	if err := m.resourceClaimManager.Start(ctx); err != nil {
		return fmt.Errorf("error creating ResourceClaim manager: %w", err)
	}

	return nil
}

func (m *ComputeDomainManager) Stop() error {
	if err := m.deploymentManager.Stop(); err != nil {
		return fmt.Errorf("error stopping Deployment manager: %w", err)
	}
	if err := m.resourceClaimManager.Stop(); err != nil {
		return fmt.Errorf("error stopping ResourceClaim manager: %w", err)
	}
	if err := m.deviceClassManager.Stop(); err != nil {
		return fmt.Errorf("error stopping DeviceClass manager: %w", err)
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

func (m *ComputeDomainManager) onComputeDomainAdd(ctx context.Context, obj any) error {
	cd, ok := obj.(*nvapi.ComputeDomain)
	if !ok {
		return fmt.Errorf("failed to cast to ComputeDomain")
	}

	klog.Infof("Processing added ComputeDomain: %s/%s", cd.Namespace, cd.Name)

	if _, err := m.deploymentManager.Create(ctx, m.config.driverNamespace, cd.Spec.NumNodes, cd); err != nil {
		return fmt.Errorf("error creating Deployment: %w", err)
	}

	dc, err := m.deviceClassManager.Create(ctx, cd.Spec.DeviceClassName, cd)
	if err != nil {
		return fmt.Errorf("error creating DeviceClass: %w", err)
	}

	for _, name := range cd.Spec.ResourceClaimNames {
		if _, err := m.resourceClaimManager.Create(ctx, cd.Namespace, name, dc.Name, cd); err != nil {
			return fmt.Errorf("error creating ResourceClaim '%s/%s': %w", cd.Namespace, name, err)
		}
	}

	return nil
}

func (m *ComputeDomainManager) onComputeDomainDelete(ctx context.Context, obj any) error {
	cd, ok := obj.(*nvapi.ComputeDomain)
	if !ok {
		return fmt.Errorf("failed to cast to ComputeDomain")
	}

	klog.Infof("Processing deleted ComputeDomain: %s/%s", cd.Namespace, cd.Name)

	if err := m.deploymentManager.Delete(ctx, string(cd.UID)); err != nil {
		return fmt.Errorf("error deleting Deployment: %w", err)
	}

	if err := m.deviceClassManager.Delete(ctx, string(cd.UID)); err != nil {
		return fmt.Errorf("error deleting DeviceClass: %w", err)
	}

	if err := m.resourceClaimManager.Delete(ctx, string(cd.UID)); err != nil {
		return fmt.Errorf("error deleting ResourceClaim: %w", err)
	}

	return nil
}
