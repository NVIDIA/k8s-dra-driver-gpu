/*
 * Copyright (c) 2024 NVIDIA CORPORATION.  All rights reserved.
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

	v1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

	nvapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
)

const (
	ResourceSliceComputeDomainChannelStart = 1    // Channel 0 is reserved, and advertised by the node itself
	ResourceSliceComputeDomainChannelLimit = 128  // There is a limit of 128 per ResourceSlice
	DriverComputeDomainChannelLimit        = 2048 // This limit is imposed by the underlying GPU driver
)

type ComputeDomainChannelManager struct {
	config        *ManagerConfig
	cancelContext context.CancelFunc

	resourceSliceComputeDomainChannelStart int
	resourceSliceComputeDomainChannelLimit int
	driverComputeDomainChannelLimit        int
	driverResources                        *resourceslice.DriverResources

	controller *resourceslice.Controller
}

func NewComputeDomainChannelManager(config *ManagerConfig) *ComputeDomainChannelManager {
	driverResources := &resourceslice.DriverResources{
		Pools: make(map[string]resourceslice.Pool),
	}

	m := &ComputeDomainChannelManager{
		config:                                 config,
		resourceSliceComputeDomainChannelStart: ResourceSliceComputeDomainChannelStart,
		resourceSliceComputeDomainChannelLimit: ResourceSliceComputeDomainChannelLimit,
		driverComputeDomainChannelLimit:        DriverComputeDomainChannelLimit,
		driverResources:                        driverResources,
		controller:                             nil, // OK, because controller.Stop() checks for nil
	}

	return m
}

// Start starts an ComputeDomainChannelManager.
func (m *ComputeDomainChannelManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping ComputeDomainChannelManager: %v", err)
			}
		}
	}()

	options := resourceslice.Options{
		DriverName: m.config.driverName,
		KubeClient: m.config.clientsets.Core,
		Resources:  m.driverResources,
	}

	controller, err := resourceslice.StartController(ctx, options)
	if err != nil {
		return fmt.Errorf("error starting resource slice controller: %w", err)
	}

	m.controller = controller

	return nil
}

// Stop stops an ComputeDomainChannelManager.
func (m *ComputeDomainChannelManager) Stop() error {
	m.cancelContext()
	m.controller.Stop()
	return nil
}

// CreateOrUpdatePool creates or updates a pool of ComputeDomain channels for the given ComputeDomain.
func (m *ComputeDomainChannelManager) CreateOrUpdatePool(cd *nvapi.ComputeDomain, nodeSelector *v1.NodeSelector) error {
	var slices []resourceslice.Slice
	for i := m.resourceSliceComputeDomainChannelStart; i < (len(cd.Spec.ResourceClaimTemplates) + m.resourceSliceComputeDomainChannelStart); i++ {
		remainingCopies := cd.Spec.NumNodes
		for j := 0; remainingCopies > 0; j++ {
			count := m.resourceSliceComputeDomainChannelLimit
			if remainingCopies < m.resourceSliceComputeDomainChannelLimit {
				count = remainingCopies
			}

			slice := m.generatePoolSlice(string(cd.UID), i, j, count)
			slices = append(slices, slice)

			remainingCopies -= count
		}
	}

	pool := resourceslice.Pool{
		NodeSelector: nodeSelector,
		Slices:       slices,
	}

	m.driverResources.Pools[string(cd.UID)] = pool
	m.controller.Update(m.driverResources)

	return nil
}

// DeletePool deletes a pool of ComnputeDomain channels for the given ComputeDomain.
func (m *ComputeDomainChannelManager) DeletePool(cdUID string) error {
	delete(m.driverResources.Pools, cdUID)
	m.controller.Update(m.driverResources)
	return nil
}

// generatePoolSlice generates the contents of a single ResourceSlice of ComputeDomain channels.
func (m *ComputeDomainChannelManager) generatePoolSlice(cdUID string, channel, sliceIndex, count int) resourceslice.Slice {
	var devices []resourceapi.Device
	for i := 0; i < count; i++ {
		d := resourceapi.Device{
			Name: fmt.Sprintf("channel-%d-%d-%d", channel, sliceIndex, i),
			Basic: &resourceapi.BasicDevice{
				Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
					"type": {
						StringValue: ptr.To("channel"),
					},
					"domain": {
						StringValue: ptr.To(cdUID),
					},
					"id": {
						IntValue: ptr.To(int64(channel)),
					},
				},
			},
		}
		devices = append(devices, d)
	}

	slice := resourceslice.Slice{
		Devices: devices,
	}

	return slice
}
