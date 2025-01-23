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
	resourcelisters "k8s.io/client-go/listers/resource/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	nvapi "github.com/NVIDIA/k8s-dra-driver/api/nvidia.com/resource/v1beta1"
)

type DeviceClassManager struct {
	config           *ManagerConfig
	waitGroup        sync.WaitGroup
	cancelContext    context.CancelFunc
	getComputeDomain GetComputeDomainFunc

	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer
	lister   resourcelisters.DeviceClassLister
}

func NewDeviceClassManager(config *ManagerConfig, getComputeDomain GetComputeDomainFunc) *DeviceClassManager {
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

	informer := factory.Resource().V1beta1().DeviceClasses().Informer()
	lister := factory.Resource().V1beta1().DeviceClasses().Lister()

	m := &DeviceClassManager{
		config:           config,
		getComputeDomain: getComputeDomain,
		factory:          factory,
		informer:         informer,
		lister:           lister,
	}

	return m
}

func (m *DeviceClassManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping DeviceClass manager: %v", err)
			}
		}
	}()

	if err := addComputeDomainLabelIndexer[*resourceapi.DeviceClass](m.informer); err != nil {
		return fmt.Errorf("error adding indexer for MulitNodeEnvironment label: %w", err)
	}

	_, err := m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			m.config.workQueue.Enqueue(obj, m.onAddOrUpdate)
		},
		UpdateFunc: func(objOld, objNew any) {
			m.config.workQueue.Enqueue(objNew, m.onAddOrUpdate)
		},
	})
	if err != nil {
		return fmt.Errorf("error adding event handlers for DeviceClass informer: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.factory.Start(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for DeviceClass failed")
	}

	return nil
}

func (m *DeviceClassManager) Stop() error {
	m.cancelContext()
	m.waitGroup.Wait()
	return nil
}

func (m *DeviceClassManager) Create(ctx context.Context, name string, cd *nvapi.ComputeDomain) (*resourceapi.DeviceClass, error) {
	dc, err := getByComputeDomainUID[*resourceapi.DeviceClass](ctx, m.informer, string(cd.UID))
	if err != nil {
		return nil, fmt.Errorf("error retrieving DeviceClass: %w", err)
	}
	if dc != nil {
		return dc, nil
	}

	deviceClass := &resourceapi.DeviceClass{
		ObjectMeta: metav1.ObjectMeta{
			Finalizers: []string{computeDomainFinalizer},
			Labels: map[string]string{
				computeDomainLabelKey: string(cd.UID),
			},
		},
		Spec: resourceapi.DeviceClassSpec{
			Selectors: []resourceapi.DeviceSelector{
				{
					CEL: &resourceapi.CELDeviceSelector{
						Expression: fmt.Sprintf("device.driver == '%s'", DriverName),
					},
				},
				{
					CEL: &resourceapi.CELDeviceSelector{
						Expression: fmt.Sprintf("device.attributes['%s'].type == 'imex-channel'", DriverName),
					},
				},
				{
					CEL: &resourceapi.CELDeviceSelector{
						Expression: fmt.Sprintf("device.attributes['%s'].domain == '%v'", DriverName, cd.UID),
					},
				},
			},
		},
	}

	if name == "" {
		deviceClass.GenerateName = cd.Name
	} else {
		deviceClass.Name = name
	}

	dc, err = m.config.clientsets.Core.ResourceV1beta1().DeviceClasses().Create(ctx, deviceClass, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("error creating DeviceClass: %w", err)
	}

	return dc, nil
}

func (m *DeviceClassManager) Delete(ctx context.Context, cdUID string) error {
	dc, err := getByComputeDomainUID[*resourceapi.DeviceClass](ctx, m.informer, cdUID)
	if err != nil {
		return fmt.Errorf("error retrieving DeviceClass: %w", err)
	}
	if dc == nil {
		return nil
	}

	if err := m.RemoveFinalizer(ctx, dc.Name); err != nil {
		return fmt.Errorf("error removing finalizer on DeviceClass '%s': %w", dc.Name, err)
	}

	err = m.config.clientsets.Core.ResourceV1beta1().DeviceClasses().Delete(ctx, dc.Name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("erroring deleting DeviceClass: %w", err)
	}

	return nil
}

func (m *DeviceClassManager) RemoveFinalizer(ctx context.Context, name string) error {
	dc, err := m.lister.Get(name)
	if err != nil && errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("error retrieving DeviceClass: %w", err)
	}

	newDC := dc.DeepCopy()

	newDC.Finalizers = []string{}
	for _, f := range dc.Finalizers {
		if f != computeDomainFinalizer {
			newDC.Finalizers = append(newDC.Finalizers, f)
		}
	}

	_, err = m.config.clientsets.Core.ResourceV1beta1().DeviceClasses().Update(ctx, newDC, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("error updating DeviceClass: %w", err)
	}

	return nil
}

func (m *DeviceClassManager) onAddOrUpdate(ctx context.Context, obj any) error {
	dc, ok := obj.(*resourceapi.DeviceClass)
	if !ok {
		return fmt.Errorf("failed to cast to DeviceClass")
	}

	dc, err := m.lister.Get(dc.Name)
	if err != nil && errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("erroring retreiving DeviceClass: %w", err)
	}

	klog.Infof("Processing added or updated DeviceClass: %s", dc.Name)

	cd, err := m.getComputeDomain(dc.Labels[computeDomainLabelKey])
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		if err := m.Delete(ctx, dc.Labels[computeDomainLabelKey]); err != nil {
			return fmt.Errorf("error deleting DeviceClass '%s': %w", dc.Name, err)
		}
		return nil
	}

	return nil
}
