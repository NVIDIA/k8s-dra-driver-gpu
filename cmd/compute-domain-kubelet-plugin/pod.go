/*
 * Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	resourcev1 "k8s.io/api/resource/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	nvapi "sigs.k8s.io/nvidia-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
)

var ErrBindingFailure = errors.New("binding failure")

type PodManager struct {
	config        *Config
	waitGroup     sync.WaitGroup
	cancelContext context.CancelFunc

	factory  informers.SharedInformerFactory
	informer cache.SharedIndexInformer
	lister   corev1listers.PodLister

	assertNamespace          AssertNameSpaceFunc
	addNodeLabel             AddNodeLabelFunc
	removeNodeLabel          RemoveNodeLabelFunc
	assertComputeDomainReady AssertComputeDomainReadyFunc
}

func NewPodManager(config *Config, assertNamespace AssertNameSpaceFunc, addNodeLabel AddNodeLabelFunc, removeNodeLabel RemoveNodeLabelFunc, assertComputeDomainReady AssertComputeDomainReadyFunc) *PodManager {
	selector := fmt.Sprintf("status.nominatedNodeName=%s", config.flags.nodeName)

	factory := informers.NewSharedInformerFactoryWithOptions(
		config.clientsets.Core,
		informerResyncPeriod,
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = selector
		}),
	)

	informer := factory.Core().V1().Pods().Informer()
	lister := factory.Core().V1().Pods().Lister()

	return &PodManager{
		config:                   config,
		factory:                  factory,
		informer:                 informer,
		lister:                   lister,
		assertNamespace:          assertNamespace,
		addNodeLabel:             addNodeLabel,
		removeNodeLabel:          removeNodeLabel,
		assertComputeDomainReady: assertComputeDomainReady,
	}
}

func (m *PodManager) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	_, err := m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			pod, ok := obj.(*corev1.Pod)
			if !ok {
				return
			}
			m.config.workQueue.EnqueueWithKey(obj, fmt.Sprintf("%s/%s", pod.Namespace, pod.Name), m.onAddOrUpdate)
		},
		UpdateFunc: func(oldObj, newObj any) {
			pod, ok := newObj.(*corev1.Pod)
			if !ok {
				return
			}
			m.config.workQueue.EnqueueWithKey(newObj, fmt.Sprintf("%s/%s", pod.Namespace, pod.Name), m.onAddOrUpdate)
		},
	})
	if err != nil {
		return fmt.Errorf("error adding event handlers for Pod informer: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.informer.Run(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for Pod failed")
	}

	return nil
}

func (m *PodManager) Stop() error {
	if m.cancelContext != nil {
		m.cancelContext()
	}
	m.waitGroup.Wait()
	return nil
}

func (m *PodManager) onAddOrUpdate(ctx context.Context, obj any) error {
	originalPod, ok := obj.(*corev1.Pod)
	if !ok {
		return fmt.Errorf("failed to cast to Pod")
	}

	// Get the latest Pod to avoid running reconcile on already deleted pods
	pod, err := m.lister.Pods(originalPod.Namespace).Get(originalPod.Name)
	if apierrors.IsNotFound(err) {
		klog.V(2).Infof("Pod (%s/%s) was removed", originalPod.Namespace, originalPod.Name)
		return nil
	}
	if err != nil {
		return fmt.Errorf("error getting Pod: %w", err)
	}

	if pod.GetDeletionTimestamp() != nil {
		return nil
	}

	klog.V(2).Infof("Processing added or updated Pod: %s/%s", pod.Namespace, pod.Name)

	// Get all ResourceClaims associated with the Pod
	rcs, err := m.GetResourceClaims(ctx, pod)
	if err != nil {
		return fmt.Errorf("error getting ResourceClaim: %w", err)
	}

	var config *nvapi.ComputeDomainChannelConfig
	var targetRC *resourcev1.ResourceClaim
	for _, rc := range rcs {
		// Extract the target ResourceClaim from the Pod's ResourceClaims and get the corresponding ComputeDomainChannelConfig
		config, err = m.getComputeDomainChannelRequestConfig(rc)
		if err != nil {
			return fmt.Errorf("error getting config for ComputeDomainChannel request from ResourceClaim %s/%s: %w", rc.Namespace, rc.Name, err)
		}
		if config != nil {
			targetRC = rc
			break
		}
	}

	// Exit if Pod does not have ResourceClaim allocated to channel device
	if config == nil {
		return nil
	}

	// Get domain id
	domainID := config.DomainID

	// Check that ComputeDomain and ResourceClaim have the same namespace
	err = m.assertNamespace(ctx, targetRC.Namespace, domainID)
	if err != nil {
		klog.Errorf("failed to assert Namespace for computeDomain with domainID %s and ResourceClaim %s/%s: %s", domainID, targetRC.Namespace, targetRC.Name, err.Error())
		return nil
	}

	// Add node label to start IMEX DaemonSet pod
	if err := m.addNodeLabel(ctx, domainID); err != nil {
		return fmt.Errorf("error adding Node label for ComputeDomain: %w", err)
	}

	// Check ComputeDomain.status.nodes.status is ready
	err = m.assertComputeDomainReady(ctx, domainID)

	switch {
	// Ready
	case err == nil:
		if err := m.SetBindingConditions(ctx, targetRC.Name, targetRC.Namespace, nvapi.ComputeDomainBindingConditions); err != nil {
			return fmt.Errorf("error setting BindingConditions to ResourceClaim: %w", err)
		}
	// Failed
	case errors.Is(err, ErrBindingFailure):
		if err := m.SetBindingConditions(ctx, targetRC.Name, targetRC.Namespace, nvapi.ComputeDomainBindingFailureConditions); err != nil {
			return fmt.Errorf("error setting BindingFailureConditions to ResourceClaim: %w", err)
		}
		if err := m.removeNodeLabel(ctx, domainID); err != nil {
			return fmt.Errorf("error removing Node label for ComputeDomain: %w", err)
		}
		klog.V(2).Infof("asserting ComputeDomain Ready: %v", err)
	default:
		return fmt.Errorf("error asserting ComputeDomain Ready: %w", err)
	}

	return nil
}

func (m *PodManager) GetResourceClaims(ctx context.Context, pod *corev1.Pod) ([]*resourcev1.ResourceClaim, error) {
	rcStatuses := pod.Status.ResourceClaimStatuses
	var rcs []*resourcev1.ResourceClaim
	for _, rcStatus := range rcStatuses {
		rc, err := m.config.clientsets.Resource.ResourceClaims(pod.Namespace).Get(ctx, *rcStatus.ResourceClaimName, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("error Get API for ResourceClaim: %w", err)
		}
		rcs = append(rcs, rc)
	}
	return rcs, nil
}

// getComputeDomainChannelResult determines if a ResourceClaim is a monitoring target by filtering
// the allocationResult and returns the config information associated with the target allocationResult.
//
// The processing target must meet the following conditions:
// - The driver is "compute-domain.nvidia.com"
// - The device is a channel device (determined by whether its corresponding config is ComputeDomainChannelConfig)
// - The device has BindingConditions
// - The device is not set BindingConditions or BindingFailureConditions
func (m *PodManager) getComputeDomainChannelRequestConfig(rc *resourcev1.ResourceClaim) (*nvapi.ComputeDomainChannelConfig, error) {
	if rc.Status.Allocation == nil || len(rc.Status.ReservedFor) == 0 {
		return nil, fmt.Errorf("error ResourceClaim has no status")
	}

	configs, err := GetOpaqueDeviceConfigs(
		nvapi.StrictDecoder,
		DriverName,
		rc.Status.Allocation.Devices.Config,
	)
	if err != nil {
		return nil, err
	}

	for _, result := range rc.Status.Allocation.Devices.Results {
		// Check the driver
		if result.Driver != DriverName {
			continue
		}
		// Check the device is channel device
		for _, c := range slices.Backward(configs) {
			if !slices.Contains(c.Requests, result.Request) {
				continue
			}

			channelConfig, ok := c.Config.(*nvapi.ComputeDomainChannelConfig)
			if !ok {
				continue
			}

			if !slices.Contains(result.BindingConditions, nvapi.ComputeDomainBindingConditions) {
				continue
			}

			if IsBindingConditionsAlreadySet(rc, &result) {
				continue
			}

			return channelConfig, nil
		}
	}

	return nil, nil
}

func (m *PodManager) SetBindingConditions(ctx context.Context, rcName, rcNamespace string, conditionType string) error {
	rc, err := m.config.clientsets.Resource.ResourceClaims(rcNamespace).Get(ctx, rcName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get ResourceClaim %s/%s: %w", rcNamespace, rcName, err)
	}
	newRC := rc.DeepCopy()
	if len(newRC.Status.Devices) == 0 {
		for _, allocationDevice := range newRC.Status.Allocation.Devices.Results {
			device := &resourcev1.AllocatedDeviceStatus{
				Driver: allocationDevice.Driver,
				Pool:   allocationDevice.Pool,
				Device: allocationDevice.Device,
			}
			newRC.Status.Devices = append(newRC.Status.Devices, *device)
		}
	}

	if len(newRC.Status.Devices) == 0 {
		return nil
	}

	var reason string
	var message string
	switch conditionType {
	case nvapi.ComputeDomainBindingConditions:
		reason = "ComputeDomainSettingsSucceeded"
		message = "binding succeeded — ComputeDomain status ready"
	case nvapi.ComputeDomainBindingFailureConditions:
		reason = "ComputeDomainSettingsFailed"
		message = "binding failed — ComputeDomain status failed"
	}

	for i := range newRC.Status.Devices {
		device := &newRC.Status.Devices[i]
		newCondition := metav1.Condition{
			Type:               conditionType,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.NewTime(time.Now()),
			Reason:             reason,
			Message:            fmt.Sprintf("Device %s: %s", device.Device, message),
		}
		conditionExists := false
		for j, existingCond := range device.Conditions {
			if existingCond.Type == conditionType {
				if existingCond.Status != newCondition.Status {
					device.Conditions[j] = newCondition
				}
				conditionExists = true
				break
			}
		}
		if !conditionExists {
			device.Conditions = append(device.Conditions, newCondition)
		}
	}

	_, err = m.config.clientsets.Resource.ResourceClaims(newRC.Namespace).UpdateStatus(ctx, newRC, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update ResourceClaim %s/%s status with binding conditions: %w", newRC.Namespace, newRC.Name, err)
	}
	return nil
}

func IsBindingConditionsAlreadySet(rc *resourcev1.ResourceClaim, allocResult *resourcev1.DeviceRequestAllocationResult) bool {
	for _, deviceStatus := range rc.Status.Devices {
		if deviceStatus.Driver == allocResult.Driver && deviceStatus.Pool == allocResult.Pool && deviceStatus.Device == allocResult.Device {
			for _, cond := range deviceStatus.Conditions {
				// Check the device is not set BindingConditions
				if cond.Type == nvapi.ComputeDomainBindingConditions && cond.Status == metav1.ConditionTrue {
					return true
				}
				// Check the device is not set BindingFailureConditions
				if cond.Type == nvapi.ComputeDomainBindingFailureConditions && cond.Status == metav1.ConditionTrue {
					return true
				}
			}
		}
	}

	return false
}
