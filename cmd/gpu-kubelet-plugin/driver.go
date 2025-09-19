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
	"time"

	resourceapi "k8s.io/api/resource/v1"
	v1beta1 "k8s.io/api/resource/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	metav1apply "k8s.io/client-go/applyconfigurations/meta/v1"
	resourceapply "k8s.io/client-go/applyconfigurations/resource/v1beta1"
	coreclientset "k8s.io/client-go/kubernetes"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/dynamic-resource-allocation/resourceslice"
	"k8s.io/klog/v2"

	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/flock"
)

// DriverPrepUprepFlockPath is the path to a lock file used to make sure
// that calls to nodePrepareResource() / nodeUnprepareResource() never
// interleave, node-globally.
const DriverPrepUprepFlockFileName = "pu.lock"

type driver struct {
	client              coreclientset.Interface
	pluginhelper        *kubeletplugin.Helper
	state               *DeviceState
	pulock              *flock.Flock
	healthcheck         *healthcheck
	deviceHealthMonitor *deviceHealthMonitor
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

	deviceHealthMonitor, err := newDeviceHealthMonitor(ctx, config, state.allocatable, state.nvdevlib)
	if err != nil {
		return nil, fmt.Errorf("start deviceHealthMonitor: %w", err)
	}

	driver.deviceHealthMonitor = deviceHealthMonitor

	if err := driver.pluginhelper.PublishResources(ctx, resources); err != nil {
		return nil, err
	}

	go driver.deviceHealthEvents(ctx, config.flags.nodeName)

	return driver, nil
}

func (d *driver) Shutdown() error {
	if d == nil {
		return nil
	}

	if d.healthcheck != nil {
		d.healthcheck.Stop()
	}

	if d.deviceHealthMonitor != nil {
		d.deviceHealthMonitor.Stop()
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
	klog.Info("Processing device health notifications")
	for {
		select {
		case <-ctx.Done():
			klog.V(6).Info("Stop processing device health notifications")
			return
		case device, ok := <-d.deviceHealthMonitor.Unhealthy():
			if !ok {
				klog.V(6).Info("Health monitor channel closed")
				return
			}

			uuid := device.GetUUID()
			klog.Warningf("Received unhealthy notification for device: %s", uuid)

			// Mark device as unhealthy in state
			d.state.MarkDeviceUnhealthy(device)

			// update resourceclaim status
			result, claimUID, err := d.findDeviceResultAndClaimUID(uuid)
			if err != nil {
				klog.Errorf("Device %s is unhealthy, but no associated ResourceClaim was found.", uuid)
				continue
			}

			claim, err := d.findClaimByUID(ctx, types.UID(claimUID))
			if err != nil {
				klog.Errorf("Failed to find ResourceClaim object for UID %s: %v", claimUID, err)
				continue
			}

			if err := d.updateDeviceConditionInClaim(ctx, claim, result); err != nil {
				klog.Errorf("Failed to update status for ResourceClaim %s/%s: %v", claim.Namespace, claim.Name, err)
			}
		}
	}
}

func (d *driver) findDeviceResultAndClaimUID(unhealthyDeviceUUID string) (*resourceapi.DeviceRequestAllocationResult, string, error) {
	d.state.Lock()
	defer d.state.Unlock()

	checkpoint, err := d.state.getCheckpoint()
	if err != nil {
		return nil, "", fmt.Errorf("unable to get checkpoint: %v", err)
	}

	for claimUID, preparedClaim := range checkpoint.V2.PreparedClaims {
		var matchingDeviceName string

	SearchDeviceGroups:
		for _, group := range preparedClaim.PreparedDevices {
			for _, device := range group.Devices {
				var currentUUID string
				var currentDeviceName string

				if device.Gpu != nil {
					currentUUID = device.Gpu.Info.UUID
					currentDeviceName = device.Gpu.Device.DeviceName
				} else if device.Mig != nil {
					currentUUID = device.Mig.Info.UUID
					currentDeviceName = device.Mig.Device.DeviceName
				}

				if currentUUID == unhealthyDeviceUUID {
					klog.V(6).Info("found matching device to claim: %v", currentDeviceName)
					matchingDeviceName = currentDeviceName
					break SearchDeviceGroups
				}
			}
		}

		if matchingDeviceName != "" {
			if preparedClaim.Status.Allocation != nil {
				for i := range preparedClaim.Status.Allocation.Devices.Results {
					result := &preparedClaim.Status.Allocation.Devices.Results[i]
					if result.Device == matchingDeviceName {
						klog.V(6).Info("Found result object: %v for claim: %s", result.Device, claimUID)
						return result, claimUID, nil
					}
				}
			}
		}
	}
	return nil, "", nil
}

func (d *driver) findClaimByUID(ctx context.Context, claimUID types.UID) (*v1beta1.ResourceClaim, error) {
	claimList, err := d.state.config.clientsets.Core.ResourceV1beta1().ResourceClaims("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ResourceClaims: %w", err)
	}

	for i := range claimList.Items {
		if claimList.Items[i].UID == claimUID {
			klog.V(6).Info("Found ResourceClaim with UID %s", claimUID)
			return &claimList.Items[i], nil
		}
	}
	return nil, fmt.Errorf("ResourceClaim with UID %s not found", claimUID)
}

func (d *driver) updateDeviceConditionInClaim(ctx context.Context, claim *v1beta1.ResourceClaim, result *resourceapi.DeviceRequestAllocationResult) error {
	klog.Infof("Applying 'Ready=False' condition for device '%s' in ResourceClaim '%s/%s'", result.Device, claim.Namespace, claim.Name)

	unhealthyCondition := metav1apply.Condition().
		WithType("Ready").
		WithStatus(metav1.ConditionFalse).
		WithReason("DeviceUnhealthy").
		WithMessage(fmt.Sprintf("Device %s has become unhealthy.", result.Device)).
		WithLastTransitionTime(metav1.Now())

	deviceStatusApplyConfig := resourceapply.AllocatedDeviceStatus().
		WithDevice(result.Device).
		WithDriver(result.Driver).
		WithPool(result.Pool).
		WithConditions(unhealthyCondition)

	claimApplyConfig := resourceapply.ResourceClaim(claim.Name, claim.Namespace).
		WithStatus(resourceapply.ResourceClaimStatus().
			WithDevices(deviceStatusApplyConfig),
		)

	opts := metav1.ApplyOptions{FieldManager: DriverName, Force: true}

	_, err := d.state.config.clientsets.Core.ResourceV1beta1().ResourceClaims(claim.Namespace).
		ApplyStatus(ctx, claimApplyConfig, opts)

	return err
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
