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
)

const (
	ResourceSliceImexChannelLimit = 128
	DriverImexChannelLimit        = 128 // 2048
)

type ImexChannelManager struct {
	config        *ManagerConfig
	cancelContext context.CancelFunc

	resourceSliceImexChannelLimit int
	driverImexChannelLimit        int
	driverResources               *resourceslice.DriverResources

	controller *resourceslice.Controller
}

func NewImexChannelManager(config *ManagerConfig) *ImexChannelManager {
	driverResources := &resourceslice.DriverResources{
		Pools: make(map[string]resourceslice.Pool),
	}

	m := &ImexChannelManager{
		config:                        config,
		resourceSliceImexChannelLimit: ResourceSliceImexChannelLimit,
		driverImexChannelLimit:        DriverImexChannelLimit,
		driverResources:               driverResources,
		controller:                    nil, // OK, because controller.Stop() checks for nil
	}

	return m
}

// Start starts an ImexChannelManager.
func (m *ImexChannelManager) Start(ctx context.Context) (rerr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	defer func() {
		if rerr != nil {
			if err := m.Stop(); err != nil {
				klog.Errorf("error stopping ImexChannel manager: %v", err)
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

// Stop stops an ImexChannelManager.
func (m *ImexChannelManager) Stop() error {
	m.cancelContext()
	m.controller.Stop()
	return nil
}

// CreateOrUpdatePool creates or updates a pool of IMEX channels for the given IMEX domain.
func (m *ImexChannelManager) CreateOrUpdatePool(imexDomainName string, nodeSelector *v1.NodeSelector) error {
	var slices []resourceslice.Slice
	for i := 0; i < m.driverImexChannelLimit; i += m.resourceSliceImexChannelLimit {
		slice := m.generatePoolSlice(imexDomainName, i, m.resourceSliceImexChannelLimit)
		slices = append(slices, slice)
	}

	pool := resourceslice.Pool{
		NodeSelector: nodeSelector,
		Slices:       slices,
	}

	m.driverResources.Pools[imexDomainName] = pool
	m.controller.Update(m.driverResources)

	return nil
}

// DeletePool deletes a pool of IMEX channels for the given IMEX domain.
func (m *ImexChannelManager) DeletePool(imexDomainName string) error {
	delete(m.driverResources.Pools, imexDomainName)
	m.controller.Update(m.driverResources)
	return nil
}

// generatePoolSlice generates the contents of a single ResourceSlice of IMEX channels in the given range.
func (m *ImexChannelManager) generatePoolSlice(imexDomainName string, startChannel, numChannels int) resourceslice.Slice {
	var devices []resourceapi.Device
	for i := startChannel; i < (startChannel + numChannels); i++ {
		d := resourceapi.Device{
			Name: fmt.Sprintf("imex-channel-%d", i),
			Basic: &resourceapi.BasicDevice{
				Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
					"type": {
						StringValue: ptr.To("imex-channel"),
					},
					"domain": {
						StringValue: ptr.To(imexDomainName),
					},
					"channel": {
						IntValue: ptr.To(int64(i)),
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
