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

	nvapi "github.com/NVIDIA/k8s-dra-driver/api/nvidia.com/resource/gpu/v1alpha1"
	nvinformers "github.com/NVIDIA/k8s-dra-driver/pkg/nvidia.com/resource/informers/externalversions"
	nvlisters "github.com/NVIDIA/k8s-dra-driver/pkg/nvidia.com/resource/listers/gpu/v1alpha1"
)

type MultiNodeEnvironmentExistsFunc func(uid string) (bool, error)

const (
	informerResyncPeriod = 10 * time.Minute

	multiNodeEnvironmentLabelKey  = "gpu.nvidia.com/multiNodeEnvironment"
	multiNodeEnvironmentFinalizer = multiNodeEnvironmentLabelKey
)

type MultiNodeEnvironmentManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  nvinformers.SharedInformerFactory
	informer cache.SharedIndexInformer
	lister   nvlisters.MultiNodeEnvironmentLister

	deploymentManager    *DeploymentManager
	deviceClassManager   *DeviceClassManager
	resourceClaimManager *ResourceClaimManager
}

// NewMultiNodeEnvironmentManager creates a new MultiNodeEnvironmentManager.
func NewMultiNodeEnvironmentManager(config *ManagerConfig) *MultiNodeEnvironmentManager {
	factory := nvinformers.NewSharedInformerFactory(config.clientsets.Nvidia, informerResyncPeriod)
	informer := factory.Gpu().V1alpha1().MultiNodeEnvironments().Informer()
	lister := nvlisters.NewMultiNodeEnvironmentLister(informer.GetIndexer())

	m := &MultiNodeEnvironmentManager{
		config:   config,
		factory:  factory,
		informer: informer,
		lister:   lister,
	}
	m.deploymentManager = NewDeploymentManager(config, m.Exists)
	m.deviceClassManager = NewDeviceClassManager(config, m.Exists)
	m.resourceClaimManager = NewResourceClaimManager(config, m.Exists)

	return m
}

// Start starts a MultiNodeEnvironmentManager.
func (m *MultiNodeEnvironmentManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping MultiNodeEnvironment manager: %v", err)
			}
		}
	}()

	err := m.informer.AddIndexers(cache.Indexers{
		"uid": uidIndexer[*nvapi.MultiNodeEnvironment],
	})
	if err != nil {
		return fmt.Errorf("error adding indexer for UIDs: %w", err)
	}

	_, err = m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			m.config.workQueue.Enqueue(obj, m.onMultiNodeEnvironmentAdd)
		},
		DeleteFunc: func(obj any) {
			m.config.workQueue.Enqueue(obj, m.onMultiNodeEnvironmentDelete)
		},
	})
	if err != nil {
		return fmt.Errorf("error adding event handlers for MultiNodeEnvironment informer: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.factory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for MultiNodeEnvironments failed")
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

func (m *MultiNodeEnvironmentManager) Stop() error {
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

// Exists checks if a MultiNodeEnvironment with a specific UID exists.
func (m *MultiNodeEnvironmentManager) Exists(uid string) (bool, error) {
	mnes, err := m.informer.GetIndexer().ByIndex("uid", uid)
	if err != nil {
		return false, fmt.Errorf("error retrieving MultiNodeInformer by UID: %w", err)
	}
	if len(mnes) == 0 {
		return false, nil
	}
	return true, nil
}

func (m *MultiNodeEnvironmentManager) onMultiNodeEnvironmentAdd(ctx context.Context, obj any) error {
	mne, ok := obj.(*nvapi.MultiNodeEnvironment)
	if !ok {
		return fmt.Errorf("failed to cast to MultiNodeEnvironment")
	}

	klog.Infof("Processing added MultiNodeEnvironment: %s/%s", mne.Namespace, mne.Name)

	if _, err := m.deploymentManager.Create(ctx, m.config.driverNamespace, mne.Spec.NumNodes, mne); err != nil {
		return fmt.Errorf("error creating Deployment: %w", err)
	}

	dc, err := m.deviceClassManager.Create(ctx, mne.Spec.DeviceClassName, mne)
	if err != nil {
		return fmt.Errorf("error creating DeviceClass: %w", err)
	}

	if mne.Spec.ResourceClaimName != "" {
		if _, err := m.resourceClaimManager.Create(ctx, mne.Namespace, mne.Spec.ResourceClaimName, dc.Name, mne); err != nil {
			return fmt.Errorf("error creating ResourceClaim '%s/%s': %w", mne.Namespace, mne.Spec.ResourceClaimName, err)
		}
	}

	return nil
}

func (m *MultiNodeEnvironmentManager) onMultiNodeEnvironmentDelete(ctx context.Context, obj any) error {
	mne, ok := obj.(*nvapi.MultiNodeEnvironment)
	if !ok {
		return fmt.Errorf("failed to cast to MultiNodeEnvironment")
	}

	klog.Infof("Processing deleted MultiNodeEnvironment: %s/%s", mne.Namespace, mne.Name)

	if err := m.deploymentManager.Delete(ctx, string(mne.UID)); err != nil {
		return fmt.Errorf("error deleting Deployment: %w", err)
	}

	if err := m.deviceClassManager.Delete(ctx, string(mne.UID)); err != nil {
		return fmt.Errorf("error deleting DeviceClass: %w", err)
	}

	if err := m.resourceClaimManager.Delete(ctx, string(mne.UID)); err != nil {
		return fmt.Errorf("error deleting ResourceClaim '%s/%s': %w", mne.Namespace, mne.Spec.ResourceClaimName, err)
	}

	return nil
}
