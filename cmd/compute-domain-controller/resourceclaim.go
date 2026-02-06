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
	"errors"
	"fmt"
	"slices"
	"sync"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	resourcev1 "k8s.io/api/resource/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

var ErrBindingFailure = errors.New("binding failure")

type OpaqueDeviceConfig struct {
	Requests []string
	Config   runtime.Object
}

type ResourceClaimManager struct {
	config           *ManagerConfig
	waitGroup        sync.WaitGroup
	cancelContext    context.CancelFunc
	informer         cache.SharedIndexInformer
	getComputeDomain GetComputeDomainFunc
}

func NewResourceClaimManager(config *ManagerConfig, getComputeDomain GetComputeDomainFunc) *ResourceClaimManager {
	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
				return config.clientsets.Resource.ResourceClaims("").List(ctx, options)
			},
			WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
				return config.clientsets.Resource.ResourceClaims("").Watch(ctx, options)
			},
		},
		&resourcev1.ResourceClaim{},
		informerResyncPeriod,
		cache.Indexers{},
	)

	m := &ResourceClaimManager{
		config:           config,
		informer:         informer,
		getComputeDomain: getComputeDomain,
	}

	return m
}

func (m *ResourceClaimManager) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	_, err := m.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			m.config.workQueue.EnqueueWithKey(obj, "resourceclaim", m.addOrUpdate)
		},
		UpdateFunc: func(oldObj, newObj any) {
			m.config.workQueue.EnqueueWithKey(newObj, "resourceclaim", m.addOrUpdate)
		},
	})
	if err != nil {
		return fmt.Errorf("error adding event handlers for ResourceClaim informer: %w", err)
	}

	m.waitGroup.Add(1)
	go func() {
		defer m.waitGroup.Done()
		m.informer.Run(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), m.informer.HasSynced) {
		return fmt.Errorf("informer cache sync for ResourceClaim failed")
	}

	return nil
}

func (m *ResourceClaimManager) Stop() error {
	if m.cancelContext != nil {
		m.cancelContext()
	}
	m.waitGroup.Wait()
	return nil
}

func (m *ResourceClaimManager) addOrUpdate(ctx context.Context, obj any) error {
	rc, ok := obj.(*resourcev1.ResourceClaim)
	if !ok {
		return fmt.Errorf("failed to cast object to ResourceClaim")
	}

	if rc.Status.Allocation == nil || len(rc.Status.ReservedFor) == 0 {
		return nil
	}

	// Check the allocationResult of ResourceClaim to determine whether it should be monitored.
	req, err := m.getComputeDomainChannelRequestConfig(rc)
	if err != nil {
		return fmt.Errorf("error getting config for ComputeDomainChannel request from ResourceClaim %s/%s: %w", rc.Namespace, rc.Name, err)
	}

	if req == nil {
		return nil
	}

	// Get domain id
	domainID := req.DomainID

	//Check namespace
	err = m.AssertComputeDomainNamespace(rc.Namespace, domainID)
	if err != nil {
		return fmt.Errorf("failed to assert Namespace for computeDomain with domainID %s and ResourceClaim %s/%s: %w", domainID, rc.Namespace, rc.Name, err)
	}

	if rc.Status.Allocation.AllocationTimestamp == nil {
		// If allocationTimestamp is nil, this specific allocation cannot be tracked uniquely
		// and we cannot reliably check timeout based on it.
		// Deciding how to handle this case; for now, consider it not trackable for polling.
		klog.V(4).Infof("ResourceClaim %s/%s has an allocation but no AllocationTimestamp. Skipping monitoring.", rc.Namespace, rc.Name)
		return nil
	}

	// Extract the node name from the allocation status
	nodeName, ok := allocatedNodeName(rc)
	if !ok {
		return fmt.Errorf("matching ResourceClaim %s/%s has no allocated nodeName", rc.Namespace, rc.Name)
	}

	cd, err := m.getComputeDomain(domainID)
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		return fmt.Errorf("ComputeDomain not found: %s", domainID)
	}
	newCD := cd.DeepCopy()

	// Check whether there is another node with the same ResourceClaim
	// If rescheduling occurs due to BindingFailureConditions or BindingTimeout, the ResourceClaim's allocationResult is updated.
	// This can cause the Pod to be assigned to a different node, so we must update the node used for ResourceClaim assignment accordingly.
	var foundRCStatus bool
	if newCD.Status.ResourceClaims != nil {
		for _, nodeRC := range newCD.Status.ResourceClaims {
			if nodeRC.Name == rc.GetName() && nodeRC.Namespace == rc.GetNamespace() {
				if nodeRC.NodeName == nodeName {
					// Do nothing if it is assigned to the same node
					klog.Infof("ResourceClaim %s/%s is already assigned to node %s in ComputeDomain %s, no update needed", rc.GetNamespace(), rc.GetName(), nodeName, domainID)
					return nil
				} else {
					// Since the nodeName differs, update the node name
					nodeRC.NodeName = nodeName
					klog.Infof("Updated NodeName for ResourceClaim %s/%s from previous node to %s in ComputeDomain %s", rc.GetNamespace(), rc.GetName(), nodeName, domainID)
				}
				foundRCStatus = true
				break
			}
		}
	}

	// If not found, add new ResourceClaim entry
	if !foundRCStatus {
		newRC := &nvapi.ComputeDomainResourceClaim{
			Name:      rc.GetName(),
			Namespace: rc.GetNamespace(),
			NodeName:  nodeName,
		}
		newCD.Status.ResourceClaims = append(newCD.Status.ResourceClaims, newRC)
		klog.Infof("Added new ResourceClaim %s/%s assigned to node %s in ComputeDomain %s", rc.GetNamespace(), rc.GetName(), nodeName, domainID)
	}

	_, err = m.config.clientsets.Nvidia.ResourceV1beta1().ComputeDomains(newCD.Namespace).UpdateStatus(ctx, newCD, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("error updating ComputeDomain status: %w", err)
	}
	klog.Infof("Successfully updated node in CD for ResourceClaim (%s/%s)", rc.GetNamespace(), rc.GetName())
	return nil
}

// getComputeDomainChannelRequestConfig determines if a ResourceClaim is a monitoring target by filtering
// the allocationResult and returns the config information associated with the target allocationResult.
//
// The processing target must meet the following conditions:
// - The driver is "compute-domain.nvidia.com"
// - The device is a channel device (determined by whether its corresponding config is ComputeDomainChannelConfig)
// - The device has BindingConditions
// - The device is not set any BindingConditions/BindingFailureConditions
func (m *ResourceClaimManager) getComputeDomainChannelRequestConfig(rc *resourcev1.ResourceClaim) (*nvapi.ComputeDomainChannelConfig, error) {
	var config *nvapi.ComputeDomainChannelConfig

	configs, err := GetOpaqueDeviceConfigs(
		nvapi.StrictDecoder,
		DriverName,
		rc.Status.Allocation.Devices.Config,
	)
	if err != nil {
		return nil, err
	}

	var configResults []*resourcev1.DeviceRequestAllocationResult
	for _, result := range rc.Status.Allocation.Devices.Results {
		// Check the driver
		if result.Driver != DriverName {
			continue
		}
		// Check the device is channel device
		for _, c := range slices.Backward(configs) {
			if slices.Contains(c.Requests, result.Request) {
				if _, ok := c.Config.(*nvapi.ComputeDomainChannelConfig); ok {
					configResults = append(configResults, &result)
					if config == nil {
						config = c.Config.(*nvapi.ComputeDomainChannelConfig)
					}
					break
				}
			}
		}
	}

	for _, result := range configResults {
		// Check channel device has BindingConditions
		if slices.Contains(result.BindingConditions, nvapi.ComputeDomainBindingConditions) {
			for _, deviceStatus := range rc.Status.Devices {
				if result.Device == deviceStatus.Device {
					for _, cond := range deviceStatus.Conditions {
						// Check the device is not set BindingConditions
						if cond.Type == nvapi.ComputeDomainBindingConditions && cond.Status == metav1.ConditionTrue {
							return nil, nil
						}
						// Check the device is not set BindingFailureConditions
						if cond.Type == nvapi.ComputeDomainBindingFailureConditions && cond.Status == metav1.ConditionTrue {
							return nil, nil
						}
					}
				}
			}
			return config, nil
		}
	}
	return nil, nil

}

func (m *ResourceClaimManager) AssertComputeDomainNamespace(claimNamespace, cdUID string) error {
	cd, err := m.getComputeDomain(cdUID)
	if err != nil {
		return fmt.Errorf("error getting ComputeDomain: %w", err)
	}
	if cd == nil {
		return fmt.Errorf("ComputeDomain not found: %s", cdUID)
	}

	if cd.Namespace != claimNamespace {
		return fmt.Errorf("the ResourceClaim's namespace is different than the ComputeDomain's namespace")
	}

	return nil
}

// GetOpaqueDeviceConfigs returns an ordered list of the configs contained in possibleConfigs for this driver.
//
// Configs can either come from the resource claim itself or from the device
// class associated with the request. Configs coming directly from the resource
// claim take precedence over configs coming from the device class. Moreover,
// configs found later in the list of configs attached to its source take
// precedence over configs found earlier in the list for that source.
//
// All of the configs relevant to the driver from the list of possibleConfigs
// will be returned in order of precedence (from lowest to highest). If no
// configs are found, nil is returned.
func GetOpaqueDeviceConfigs(
	decoder runtime.Decoder,
	driverName string,
	possibleConfigs []resourcev1.DeviceAllocationConfiguration,
) ([]*OpaqueDeviceConfig, error) {
	// Collect all configs in order of reverse precedence.
	var classConfigs []resourcev1.DeviceAllocationConfiguration
	var claimConfigs []resourcev1.DeviceAllocationConfiguration
	var candidateConfigs []resourcev1.DeviceAllocationConfiguration
	for _, config := range possibleConfigs {
		switch config.Source {
		case resourcev1.AllocationConfigSourceClass:
			classConfigs = append(classConfigs, config)
		case resourcev1.AllocationConfigSourceClaim:
			claimConfigs = append(claimConfigs, config)
		default:
			return nil, fmt.Errorf("invalid config source: %v", config.Source)
		}
	}
	candidateConfigs = append(candidateConfigs, classConfigs...)
	candidateConfigs = append(candidateConfigs, claimConfigs...)

	// Decode all configs that are relevant for the driver.
	var resultConfigs []*OpaqueDeviceConfig
	for _, config := range candidateConfigs {
		// If this is nil, the driver doesn't support some future API extension
		// and needs to be updated.
		if config.Opaque == nil {
			return nil, fmt.Errorf("only opaque parameters are supported by this driver")
		}

		// Configs for different drivers may have been specified because a
		// single request can be satisfied by different drivers. This is not
		// an error -- drivers must skip over other driver's configs in order
		// to support this.
		if config.Opaque.Driver != driverName {
			continue
		}

		decodedConfig, err := runtime.Decode(decoder, config.Opaque.Parameters.Raw)
		if err != nil {
			// Bad opaque config: i) do not retry preparing this resource
			// internally and ii) return notion of permanent error to kubelet,
			// to give it an opportunity to play this error back to the user so
			// that it becomes actionable.
			return nil, fmt.Errorf("error decoding config parameters: %w", err)
		}

		resultConfig := &OpaqueDeviceConfig{
			Requests: config.Requests,
			Config:   decodedConfig,
		}

		resultConfigs = append(resultConfigs, resultConfig)
	}

	return resultConfigs, nil
}

// Extract the allocated node name from the ResourceClaim, if present.
func allocatedNodeName(rc *resourcev1.ResourceClaim) (string, bool) {
	if rc == nil || rc.Status.Allocation == nil || rc.Status.Allocation.NodeSelector == nil {
		return "", false
	}

	for _, term := range rc.Status.Allocation.NodeSelector.NodeSelectorTerms {
		for _, field := range term.MatchFields {
			if field.Key == "metadata.name" &&
				field.Operator == "In" &&
				len(field.Values) > 0 &&
				field.Values[0] != "" {
				return field.Values[0], true
			}
		}
	}
	return "", false
}
