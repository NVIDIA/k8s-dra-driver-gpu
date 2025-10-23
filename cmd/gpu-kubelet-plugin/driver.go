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
	"path/filepath"
	"sync"
	"time"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	coreclientset "k8s.io/client-go/kubernetes"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/klog/v2"

	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/flock"
)

// DriverPrepUprepFlockPath is the path to a lock file used to make sure
// that calls to nodePrepareResource() / nodeUnprepareResource() never
// interleave, node-globally.
const DriverPrepUprepFlockFileName = "pu.lock"

type driver struct {
	client                  coreclientset.Interface
	pluginhelper            *kubeletplugin.Helper
	state                   *DeviceState
	pulock                  *flock.Flock
	healthcheck             *healthcheck
	nvmlDeviceHealthMonitor *nvmlDeviceHealthMonitor
	wg                      sync.WaitGroup
}

func NewDriver(ctx context.Context, config *Config) (*driver, error) {
	state, err := NewDeviceState(ctx, config)
	if err != nil {
		return nil, err
	}

	puLockPath := filepath.Join(config.DriverPluginPath(), DriverPrepUprepFlockFileName)

	driver := &driver{
		client: config.clientsets.Core,
		state:  state,
		pulock: flock.NewFlock(puLockPath),
	}

	helper, err := kubeletplugin.Start(
		ctx,
		driver,
		kubeletplugin.KubeClient(driver.client),
		kubeletplugin.NodeName(config.flags.nodeName),
		kubeletplugin.DriverName(DriverName),
		kubeletplugin.Serialize(false),
		kubeletplugin.RegistrarDirectoryPath(config.flags.kubeletRegistrarDirectoryPath),
		kubeletplugin.PluginDataDirectoryPath(config.DriverPluginPath()),
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

	healthcheck, err := startHealthcheck(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("start healthcheck: %w", err)
	}
	driver.healthcheck = healthcheck

	if featuregates.Enabled(featuregates.DeviceHealthCheck) {
		nvmlDeviceHealthMonitor, err := newNvmlDeviceHealthMonitor(ctx, config, state.allocatable, state.nvdevlib)
		if err != nil {
			return nil, fmt.Errorf("start nvmlDeviceHealthMonitor: %w", err)
		}

		driver.nvmlDeviceHealthMonitor = nvmlDeviceHealthMonitor

		driver.wg.Add(1)
		go func() {
			defer driver.wg.Done()
			driver.deviceHealthEvents(ctx, config.flags.nodeName)
		}()
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

	if d.healthcheck != nil {
		d.healthcheck.Stop()
	}

	if d.nvmlDeviceHealthMonitor != nil {
		d.nvmlDeviceHealthMonitor.Stop()
	}

	d.wg.Wait()

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

func (d *driver) HandleError(ctx context.Context, err error, msg string) {
	// For now we just follow the advice documented in the DRAPlugin API docs.
	// See: https://pkg.go.dev/k8s.io/apimachinery/pkg/util/runtime#HandleErrorWithContext
	runtime.HandleErrorWithContext(ctx, err, msg)
}

func (d *driver) nodePrepareResource(ctx context.Context, claim *resourceapi.ResourceClaim) kubeletplugin.PrepareResult {
	release, err := d.pulock.Acquire(ctx, flock.WithTimeout(10*time.Second))
	if err != nil {
		return kubeletplugin.PrepareResult{
			Err: fmt.Errorf("error acquiring prep/unprep lock: %w", err),
		}
	}
	defer release()

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
	release, err := d.pulock.Acquire(ctx, flock.WithTimeout(10*time.Second))
	if err != nil {
		return fmt.Errorf("error acquiring prep/unprep lock: %w", err)
	}
	defer release()

	if err := d.state.Unprepare(ctx, string(claimNs.UID)); err != nil {
		return fmt.Errorf("error unpreparing devices for claim %v: %w", claimNs.UID, err)
	}

	return nil
}

func (d *driver) deviceHealthEvents(ctx context.Context, nodeName string) {
	klog.Info("Starting to watch for device health notifications")
	for {
		select {
		case <-ctx.Done():
			klog.V(6).Info("Stop processing device health notifications")
			return
		case device, ok := <-d.nvmlDeviceHealthMonitor.Unhealthy():
			if !ok {
				klog.V(6).Info("Health monitor channel closed")
				return
			}
			uuid := device.UUID()

			klog.Warningf("Received unhealthy notification for device: %s", uuid)

			if !device.IsHealthy() {
				klog.V(6).Infof("Device: %s is aleady marked unhealthy. Skip republishing ResourceSlice", uuid)
				continue
			}

			release, err := d.pulock.Acquire(ctx, flock.WithTimeout(10*time.Second))
			if err != nil {
				klog.Errorf("error acquiring prep/unprep lock for health status update: %v", err)
				continue
			}

			// Mark device as unhealthy.
			d.state.UpdateDeviceHealthStatus(device, Unhealthy)

			// Republish resource slice with only healthy devices
			// There is no remediation loop right now meaning if the unhealthy device is fixed,
			// driver needs to be restarted to publish the ResourceSlice with all devices
			var resourceSlice resourceslice.Slice
			for _, dev := range d.state.allocatable {
				uuid := dev.UUID()
				if dev.IsHealthy() {
					klog.V(6).Infof("Device: %s is healthy, added to ResoureSlice", uuid)
					resourceSlice.Devices = append(resourceSlice.Devices, dev.GetDevice())
				} else {
					klog.Warningf("Device: %s is unhealthy, will be removed from ResoureSlice", uuid)
				}
			}

			klog.V(6).Info("Rebulishing resourceslice with healthy devices")
			resources := resourceslice.DriverResources{
				Pools: map[string]resourceslice.Pool{
					nodeName: {Slices: []resourceslice.Slice{resourceSlice}},
				},
			}

			if err := d.pluginhelper.PublishResources(ctx, resources); err != nil {
				klog.Errorf("Failed to publish resources after device health status update: %v", err)
			} else {
				klog.V(6).Info("Successfully republished resources without unhealthy device")
			}

			release()
		}
	}
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
