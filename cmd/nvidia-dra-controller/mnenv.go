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

	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	resourcelisters "k8s.io/client-go/listers/resource/v1beta1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	nvapi "github.com/NVIDIA/k8s-dra-driver/api/nvidia.com/resource/gpu/v1alpha1"
	"github.com/NVIDIA/k8s-dra-driver/pkg/flags"
	nvinformers "github.com/NVIDIA/k8s-dra-driver/pkg/nvidia.com/resource/informers/externalversions"
	nvlisters "github.com/NVIDIA/k8s-dra-driver/pkg/nvidia.com/resource/listers/gpu/v1alpha1"
	"github.com/NVIDIA/k8s-dra-driver/pkg/workqueue"
)

const (
	multiNodeEnvironmentFinalizer = "gpu.nvidia.com/finalizer.multiNodeEnvironment"
	imexDeviceClass               = "imex.nvidia.com"
)

type MultiNodeEnvironmentManager struct {
	clientsets flags.ClientSets
	waitGroup  sync.WaitGroup

	multiNodeEnvironmentInformer cache.SharedIndexInformer
	multiNodeEnvironmentLister   nvlisters.MultiNodeEnvironmentLister
	resourceClaimLister          resourcelisters.ResourceClaimLister
}

// StartManager starts a MultiNodeEnvironmentManager.
func StartMultiNodeEnvironmentManager(ctx context.Context, config *Config) (*MultiNodeEnvironmentManager, error) {
	queue := workqueue.New(workqueue.DefaultControllerRateLimiter())

	nvInformerFactory := nvinformers.NewSharedInformerFactory(config.clientsets.Nvidia, 30*time.Second)
	coreInformerFactory := informers.NewSharedInformerFactory(config.clientsets.Core, 30*time.Second)

	mneInformer := nvInformerFactory.Gpu().V1alpha1().MultiNodeEnvironments().Informer()
	mneLister := nvlisters.NewMultiNodeEnvironmentLister(mneInformer.GetIndexer())

	rcInformer := coreInformerFactory.Resource().V1beta1().ResourceClaims().Informer()
	rcLister := resourcelisters.NewResourceClaimLister(rcInformer.GetIndexer())

	m := &MultiNodeEnvironmentManager{
		clientsets:                   config.clientsets,
		multiNodeEnvironmentInformer: mneInformer,
		multiNodeEnvironmentLister:   mneLister,
		resourceClaimLister:          rcLister,
	}

	var err error
	err = mneInformer.AddIndexers(cache.Indexers{
		"uid": func(obj interface{}) ([]string, error) {
			mne, ok := obj.(*nvapi.MultiNodeEnvironment)
			if !ok {
				return nil, fmt.Errorf("expected a MultiNodeEnvironment but got %T", obj)
			}
			return []string{string(mne.UID)}, nil
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error adding indexer for MultiNodeEnvironment UUIDs: %w", err)
	}

	_, err = mneInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) { queue.Enqueue(obj, m.onMultiNodeEnvironmentAdd) },
	})
	if err != nil {
		return nil, fmt.Errorf("error adding event handlers for MultiNodeEnvironment informer: %w", err)
	}

	_, err = rcInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj any) { queue.Enqueue(obj, m.onResourceClaimAddOrUpdate) },
		UpdateFunc: func(objOld, objNew any) { queue.Enqueue(objNew, m.onResourceClaimAddOrUpdate) },
	})
	if err != nil {
		return nil, fmt.Errorf("error adding event handlers for ResourceClaim informer: %w", err)
	}

	m.waitGroup.Add(3)
	go func() {
		defer m.waitGroup.Done()
		nvInformerFactory.Start(ctx.Done())
	}()
	go func() {
		defer m.waitGroup.Done()
		coreInformerFactory.Start(ctx.Done())
	}()
	go func() {
		defer m.waitGroup.Done()
		queue.Run(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), mneInformer.HasSynced, rcInformer.HasSynced) {
		klog.Warning("Cache sync failed; retrying in 5 seconds")
		time.Sleep(5 * time.Second)
		if !cache.WaitForCacheSync(ctx.Done(), mneInformer.HasSynced, rcInformer.HasSynced) {
			return nil, fmt.Errorf("informer cache sync failed twice")
		}
	}

	return m, nil
}

// Stop stops a running MultiNodeEnvironmentManager.
func (m *MultiNodeEnvironmentManager) Stop() error {
	if m == nil {
		return nil
	}
	m.waitGroup.Wait()
	return nil
}

func (m *MultiNodeEnvironmentManager) onMultiNodeEnvironmentAdd(obj any) error {
	mne, ok := obj.(*nvapi.MultiNodeEnvironment)
	if !ok {
		return fmt.Errorf("failed to cast to MultiNodeEnvironment")
	}

	klog.Infof("Processing added MultiNodeEnvironment: %s/%s", mne.Namespace, mne.Name)

	gvk := nvapi.SchemeGroupVersion.WithKind("MultiNodeEnvironment")
	mne.APIVersion = gvk.GroupVersion().String()
	mne.Kind = gvk.Kind

	ownerReference := metav1.OwnerReference{
		APIVersion: mne.APIVersion,
		Kind:       mne.Kind,
		Name:       mne.Name,
		UID:        mne.UID,
		Controller: ptr.To(true),
	}

	if _, err := m.createResourceClaim(mne.Namespace, mne.Spec.ResourceClaimName, ownerReference); err != nil {
		return fmt.Errorf("error creating ResourceClaim '%s/%s': %w", mne.Namespace, mne.Spec.ResourceClaimName, err)
	}

	return nil
}

func (m *MultiNodeEnvironmentManager) onResourceClaimAddOrUpdate(obj any) error {
	rc, ok := obj.(*resourceapi.ResourceClaim)
	if !ok {
		return fmt.Errorf("failed to cast to ResourceClaim")
	}

	klog.Infof("Processing added or updated ResourceClaim: %s/%s", rc.Namespace, rc.Name)

	if len(rc.OwnerReferences) != 1 {
		return nil
	}

	if rc.OwnerReferences[0].Kind != nvapi.MultiNodeEnvironmentKind {
		return nil
	}

	if !cache.WaitForCacheSync(context.Background().Done(), m.multiNodeEnvironmentInformer.HasSynced) {
		return fmt.Errorf("cache sync failed for MultiNodeEnvironment")
	}

	mnes, err := m.multiNodeEnvironmentInformer.GetIndexer().ByIndex("uid", string(rc.OwnerReferences[0].UID))
	if err != nil {
		return fmt.Errorf("error retrieving MultiNodeInformer OwnerReference by UID from indexer: %w", err)
	}
	if len(mnes) != 0 {
		return nil
	}

	if err := m.removeResourceClaimFinalizer(rc.Namespace, rc.Name); err != nil {
		return fmt.Errorf("error removing finalizer on ResourceClaim '%s/%s': %w", rc.Namespace, rc.Name, err)
	}

	if err := m.deleteResourceClaim(rc.Namespace, rc.Name); err != nil {
		return fmt.Errorf("error deleting ResourceClaim '%s/%s': %w", rc.Namespace, rc.Name, err)
	}

	return nil
}

func (m *MultiNodeEnvironmentManager) createResourceClaim(namespace, name string, ownerReference metav1.OwnerReference) (*resourceapi.ResourceClaim, error) {
	rc, err := m.resourceClaimLister.ResourceClaims(namespace).Get(name)
	if err == nil {
		if len(rc.OwnerReferences) != 1 && rc.OwnerReferences[0] != ownerReference {
			return nil, fmt.Errorf("ResourceClaim '%s/%s' exists without expected OwnerReference: %v", namespace, name, ownerReference)
		}
		return rc, nil
	}
	if !errors.IsNotFound(err) {
		return nil, fmt.Errorf("error retrieving ResourceClaim: %w", err)
	}

	resourceClaim := &resourceapi.ResourceClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       namespace,
			OwnerReferences: []metav1.OwnerReference{ownerReference},
			Finalizers:      []string{multiNodeEnvironmentFinalizer},
		},
		Spec: resourceapi.ResourceClaimSpec{
			Devices: resourceapi.DeviceClaim{
				Requests: []resourceapi.DeviceRequest{{
					Name: "imex", DeviceClassName: imexDeviceClass,
				}},
			},
		},
	}

	rc, err = m.clientsets.Core.ResourceV1beta1().ResourceClaims(resourceClaim.Namespace).Create(context.Background(), resourceClaim, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("error creating ResourceClaim: %w", err)
	}

	return rc, nil
}

func (m *MultiNodeEnvironmentManager) removeResourceClaimFinalizer(namespace, name string) error {
	rc, err := m.resourceClaimLister.ResourceClaims(namespace).Get(name)
	if err != nil && errors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("error retrieving ResourceClaim: %w", err)
	}

	newRC := rc.DeepCopy()

	newRC.Finalizers = []string{}
	for _, f := range rc.Finalizers {
		if f != multiNodeEnvironmentFinalizer {
			newRC.Finalizers = append(newRC.Finalizers, f)
		}
	}

	_, err = m.clientsets.Core.ResourceV1beta1().ResourceClaims(namespace).Update(context.Background(), newRC, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("error updating ResourceClaim: %w", err)
	}

	return nil
}

func (m *MultiNodeEnvironmentManager) deleteResourceClaim(namespace, name string) error {
	err := m.clientsets.Core.ResourceV1beta1().ResourceClaims(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("erroring deleting ResourceClaim: %w", err)
	}
	return nil
}
