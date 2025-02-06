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

	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
)

type ResourceClaimManager struct {
	config        *ManagerConfig
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer
}

func NewResourceClaimManager(config *ManagerConfig) *ResourceClaimManager {
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
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.LabelSelector = metav1.FormatLabelSelector(labelSelector)
		}),
	)

	informer := factory.Resource().V1beta1().ResourceClaims().Informer()

	m := &ResourceClaimManager{
		config:   config,
		factory:  factory,
		informer: informer,
	}

	return m
}

func (m *ResourceClaimManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping ResourceClaim manager: %v", err)
			}
		}
	}()

	if err := addComputeDomainLabelIndexer[*resourceapi.ResourceClaim](m.informer); err != nil {
		return fmt.Errorf("error adding indexer for MulitNodeEnvironment label: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.factory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for ResourceClaim failed")
	}

	return nil
}

func (m *ResourceClaimManager) Stop() error {
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

func (m *ResourceClaimManager) Create(ctx context.Context, namespace, name, deviceClassName string, cd *nvapi.ComputeDomain) (*resourceapi.ResourceClaim, error) {
	rc, err := m.config.clientsets.Core.ResourceV1beta1().ResourceClaims(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return nil, fmt.Errorf("error getting ResourceClaim: %w", err)
	}
	if err == nil && rc.Labels[computeDomainLabelKey] != string(cd.UID) {
		return nil, fmt.Errorf("existing ResourceClaim '%s/%s' not associated with ComputeDomain '%v'", namespace, name, cd.UID)
	}
	if err == nil {
		return rc, nil
	}

	resourceClaim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  namespace,
			Finalizers: []string{computeDomainFinalizer},
			Labels: map[string]string{
				computeDomainLabelKey: string(cd.UID),
			},
		},
		Spec: resourceapi.ResourceClaimSpec{
			Devices: resourceapi.DeviceClaim{
				Requests: []resourceapi.DeviceRequest{{
					Name: "device", DeviceClassName: deviceClassName,
				}},
			},
		},
	}

	rc, err = m.config.clientsets.Core.ResourceV1beta1().ResourceClaims(resourceClaim.Namespace).Create(ctx, resourceClaim, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("error creating ResourceClaim: %w", err)
	}

	return rc, nil
}

func (m *ResourceClaimManager) Delete(ctx context.Context, cdUID string) error {
	rcs, err := getByComputeDomainUID[*resourceapi.ResourceClaim](ctx, m.informer, cdUID)
	if err != nil {
		return fmt.Errorf("error retrieving ResourceClaims: %w", err)
	}
	if len(rcs) == 0 {
		return nil
	}

	for _, rc := range rcs {
		if rc.GetDeletionTimestamp() != nil {
			continue
		}

		err := m.config.clientsets.Core.ResourceV1beta1().ResourceClaims(rc.Namespace).Delete(ctx, rc.Name, metav1.DeleteOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("erroring deleting ResourceClaim: %w", err)
		}
	}

	return nil
}

func (m *ResourceClaimManager) RemoveFinalizer(ctx context.Context, cdUID string) error {
	rcs, err := getByComputeDomainUID[*resourceapi.ResourceClaim](ctx, m.informer, cdUID)
	if err != nil {
		return fmt.Errorf("error retrieving ResourceClaims: %w", err)
	}
	if len(rcs) == 0 {
		return nil
	}

	for _, rc := range rcs {
		if rc.GetDeletionTimestamp() == nil {
			return fmt.Errorf("attempting to remove finalizer before ResoureClaim marked for deletion")
		}

		newRC := rc.DeepCopy()
		newRC.Finalizers = []string{}
		for _, f := range rc.Finalizers {
			if f != computeDomainFinalizer {
				newRC.Finalizers = append(newRC.Finalizers, f)
			}
		}
		if len(rc.Finalizers) == len(newRC.Finalizers) {
			return nil
		}

		if _, err = m.config.clientsets.Core.ResourceV1beta1().ResourceClaims(rc.Namespace).Update(ctx, newRC, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("error updating ResourceClaim: %w", err)
		}
	}

	return nil
}
