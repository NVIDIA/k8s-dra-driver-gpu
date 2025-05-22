/*
 * Copyright (c) 2022-2024, NVIDIA CORPORATION.  All rights reserved.
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
	"k8s.io/apimachinery/pkg/types"
	coreclientset "k8s.io/client-go/kubernetes"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/klog/v2"
)

type driver struct {
	sync.Mutex
	client       coreclientset.Interface
	pluginhelper *kubeletplugin.Helper
	state        *DeviceState
}

func NewDriver(ctx context.Context, config *Config) (*driver, error) {
	state, err := NewDeviceState(ctx, config)
	if err != nil {
		return nil, err
	}
	driver := &driver{
		client: config.clientsets.Core,
		state:  state,
	}

	helper, err := kubeletplugin.Start(
		ctx,
		driver,
		kubeletplugin.KubeClient(driver.client),
		kubeletplugin.NodeName(config.flags.nodeName),
		kubeletplugin.DriverName(DriverName),
		kubeletplugin.Serialize(false),
	)
	if err != nil {
		return nil, err
	}
	driver.pluginhelper = helper

	// Enumerate the set of GPU and MIG devices and publish them
	var resourceSlice resourceslice.Slice
	for _, device := range state.allocatable {
		resourceSlice.Devices = append(resourceSlice.Devices, device.GetDevice())
	}

	resources := resourceslice.DriverResources{
		Pools: map[string]resourceslice.Pool{
			config.flags.nodeName: {Slices: []resourceslice.Slice{resourceSlice}},
		},
	}

	if err := driver.pluginhelper.PublishResources(ctx, resources); err != nil {
		return nil, err
	}

	return driver, nil
}

func (d *driver) Shutdown() error {
	if d == nil {
		return nil
	}
	d.pluginhelper.Stop()
	return nil
}

func (d *driver) PrepareResourceClaims(ctx context.Context, claims []*resourceapi.ResourceClaim) (map[types.UID]kubeletplugin.PrepareResult, error) {
	klog.V(6).Infof("PrepareResourceClaims called with %d claim(s)", len(claims))
	results := make(map[types.UID]kubeletplugin.PrepareResult)

	for _, claim := range claims {
		results[claim.UID] = d.nodePrepareResource(ctx, claim)
	}

	return results, nil
}

func (d *driver) UnprepareResourceClaims(ctx context.Context, claimRefs []kubeletplugin.NamespacedObject) (map[types.UID]error, error) {
	klog.V(6).Infof("UnprepareResourceClaims called with %d claim(s)", len(claimRefs))

	results := make(map[types.UID]error)

	for _, claimRef := range claimRefs {
		results[claimRef.UID] = d.nodeUnprepareResource(ctx, claimRef)
	}

	return results, nil
}

func (d *driver) nodePrepareResource(ctx context.Context, claim *resourceapi.ResourceClaim) kubeletplugin.PrepareResult {
	d.Lock()
	defer d.Unlock()

	devs, err := d.state.Prepare(ctx, claim)

	if err != nil {
		return kubeletplugin.PrepareResult{
			Err: fmt.Errorf("error preparing devices for claim %v: %w", claim.UID, err),
		}
	}

	klog.Infof("Returning newly prepared devices for claim '%v': %v", claim.UID, devs)
	return kubeletplugin.PrepareResult{Devices: devs}
}

func (d *driver) nodeUnprepareResource(ctx context.Context, claimNs kubeletplugin.NamespacedObject) error {
	d.Lock()
	defer d.Unlock()

	if err := d.state.Unprepare(ctx, string(claimNs.UID)); err != nil {
		return fmt.Errorf("error unpreparing devices for claim %v: %w", claimNs.UID, err)
	}

	return nil
}

// TODO: implement loop to remove CDI files from the CDI path for claimUIDs
//       that have been removed from the AllocatedClaims map.
// func (d *driver) cleanupCDIFiles(wg *sync.WaitGroup) chan error {
// 	errors := make(chan error)
// 	return errors
// }
//
// TODO: implement loop to remove mpsControlDaemon folders from the mps
//       path for claimUIDs that have been removed from the AllocatedClaims map.
// func (d *driver) cleanupMpsControlDaemonArtifacts(wg *sync.WaitGroup) chan error {
// 	errors := make(chan error)
// 	return errors
// }
