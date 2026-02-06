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
	"maps"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/Masterminds/semver"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/version"
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

type deviceHealthMonitor interface {
	Start(context.Context) error
	Stop()
	Unhealthy() <-chan *AllocatableDevice
}

type driver struct {
	client              coreclientset.Interface
	pluginhelper        *kubeletplugin.Helper
	state               *DeviceState
	pulock              *flock.Flock
	healthcheck         *healthcheck
	deviceHealthMonitor deviceHealthMonitor
	wg                  sync.WaitGroup
	// Idicates whether to use separate ResourceSlices for SharedCounters and
	// Devices (required for k8s 1.35+) or combined SharedCounters and Devices
	// in the same slice (required for k8s 1.34).
	useSplitResourceSlices bool
}

func NewDriver(ctx context.Context, config *Config) (*driver, error) {
	state, err := NewDeviceState(ctx, config)
	if err != nil {
		return nil, err
	}

	useSplitSlices := false

	if featuregates.Enabled(featuregates.DynamicMIG) {
		// Could be done in NewDeviceState, but I want to make sure that the
		// checkpoint machinery is ready to use -- that's more obvious here.
		//
		// Generally, when `featuregates.DynamicMIG` is enabled, we have to make
		// difficult but good decisions about incarnated MIG devices found
		// during program startup. We could
		//
		// 1) assume they are under control of an external entity, and not
		// announce them. That's likely not true. As hard as we try, as part of
		// dynamic MIG device management, given enough time and circumstances,
		// we might actually leave a MIG device behind where we shouldn't (as of
		// bugs, as of aggressive operations / admin intervention, ...).
		//
		// 2) not do anythin special: not good; we would still announce the
		// corresponding abstract MIG device and once the scheduler assigns a
		// job, a relevant NodePrepareResources() call will try to create that
		// specific MIG device. And that will fail, because that MIG device
		// already exists -- users see something like "prepare devices failed:
		// error creating MIG device: error creating GPU instance for
		// 'gpu-0-mig-1g24gb-0': Insufficient Resources.
		//
		// 3) Use the node-local checkpoint as the source of truth. Any MIG
		// device that corresponds to "partially prepared" claims should be
		// destroyed, and any MIG device that is not mentioned in the checkpoint
		// at all must be destroyed). Both is done below. Only those of
		// completely prepared claims can stay; assuming that the central
		// scheduler state is equivalent. TODO: review if this logic is correct;
		// or if it potentially is too invasive for certain edge cases.
		state.DestroyUnknownMIGDevices(ctx)

		// Read Kubernetes API server version to determine which ResourceSlice
		// model to use.
		var err error
		useSplitSlices, err = shouldUseSplitResourceSlices(config.clientsets.Core)
		if err != nil {
			return nil, fmt.Errorf("failed to determine ResourceSlice model: %w", err)
		}
	}

	puLockPath := filepath.Join(config.DriverPluginPath(), DriverPrepUprepFlockFileName)

	driver := &driver{
		client:                 config.clientsets.Core,
		state:                  state,
		pulock:                 flock.NewFlock(puLockPath),
		useSplitResourceSlices: useSplitSlices,
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

	healthcheck, err := startHealthcheck(ctx, config, helper)
	if err != nil {
		return nil, fmt.Errorf("start healthcheck: %w", err)
	}
	driver.healthcheck = healthcheck

	if featuregates.Enabled(featuregates.NVMLDeviceHealthCheck) {
		deviceHealthMonitor, err := newNvmlDeviceHealthMonitor(config, state.allocatable, state.nvdevlib)
		if err != nil {
			return nil, fmt.Errorf("failed to create NVML device health monitor: %w", err)
		}
		if err := deviceHealthMonitor.Start(ctx); err != nil {
			return nil, fmt.Errorf("failed to start device health monitor: %w", err)
		}
		driver.deviceHealthMonitor = deviceHealthMonitor

		driver.wg.Add(1)
		go func() {
			defer driver.wg.Done()
			driver.deviceHealthEvents(ctx, config.flags.nodeName)
		}()
	}

	// Pass `nodeUnprepareResource` function to the cleanup manager.
	if err := state.checkpointCleanupManager.Start(ctx, driver.nodeUnprepareResource); err != nil {
		return nil, fmt.Errorf("error starting CheckpointCleanupManager: %w", err)
	}

	if err := driver.publishResources(ctx, config); err != nil {
		return nil, err
	}

	klog.V(4).Infof("Current kubelet plugin registration status: %s", helper.RegistrationStatus())

	return driver, nil
}

// GenerateDriverResources() returns the set of DRA ResourceSlices announced by
// this DRA driver to the system, using the Partitionable Devices paradigm.
func (d *driver) GenerateDriverResources(nodeName string) resourceslice.DriverResources {
	if d.useSplitResourceSlices {
		return d.generateSplitResourceSlices(nodeName)
	}
	return d.generateCombinedResourceSlices(nodeName)
}

// generateSplitResourceSlices generates ResourceSlices for DynamicMIG for k8s 1.35+.
// Creates G+1 resource slices for G physical GPUs:
// - One slice with all SharedCounters (one counter set per GPU).
// - For each GPU, one slice with devices only (full GPU + MIG partitions).
func (d *driver) generateSplitResourceSlices(nodeName string) resourceslice.DriverResources {
	var gpuslices []resourceslice.Slice
	var allCounterSets []resourceapi.CounterSet

	// Iterate through `perGPUAllocatable` map in predictable order
	for _, minor := range slices.Sorted(maps.Keys(d.state.perGPUAllocatable)) {
		allocatable := d.state.perGPUAllocatable[minor]
		var deviceSlice resourceslice.Slice

		// Stable sort order by devicename
		for _, devname := range slices.Sorted(maps.Keys(allocatable)) {
			device := allocatable[devname]
			klog.V(4).Infof("About to announce device %s", devname)

			// Full GPU: collect its counter sets for the `sharedCountersSlice`.
			if device.Gpu != nil {
				allCounterSets = append(allCounterSets, device.Gpu.PartSharedCounterSets()...)
			}

			// Add device/partition to the device-only slice for this GPU.
			deviceSlice.Devices = append(deviceSlice.Devices, device.PartGetDevice())
		}
		gpuslices = append(gpuslices, deviceSlice)
	}

	sharedCountersSlice := resourceslice.Slice{
		SharedCounters: allCounterSets,
	}

	// Emit the `sharedCountersSlice` first.
	gpuslices = append([]resourceslice.Slice{sharedCountersSlice}, gpuslices...)

	return resourceslice.DriverResources{
		Pools: map[string]resourceslice.Pool{
			nodeName: {Slices: gpuslices},
		},
	}
}

// generateCombinedResourceSlices generates ResourceSlices for DynamicMIG for k8s 1.34.
// Creates G resource slices for G physical GPUs, each containing both,
// SharedCounters and Devices.
func (d *driver) generateCombinedResourceSlices(nodeName string) resourceslice.DriverResources {
	var gpuslices []resourceslice.Slice

	// Iterate through `perGPUAllocatable` map in predictable order so that the
	// slices get published in predictable order.
	for _, minor := range slices.Sorted(maps.Keys(d.state.perGPUAllocatable)) {
		allocatable := d.state.perGPUAllocatable[minor]
		var slice resourceslice.Slice
		countersets := []resourceapi.CounterSet{}

		// Stable sort order by devicename -- makes the order of devices
		// presented in a resource slice reproducible. Good for debuggability /
		// readability, and leads to a minimal slice diff during kubelet plugin
		// restart (the slice diff is logged).
		for _, devname := range slices.Sorted(maps.Keys(allocatable)) {
			device := allocatable[devname]
			klog.V(4).Infof("About to announce device %s", devname)

			// Full GPU: take note of countersets, indicating absolute capacity.
			// For now this is expected to be one counter set.
			if device.Gpu != nil {
				countersets = append(countersets, device.Gpu.PartSharedCounterSets()...)
			}

			// Add all allocatable devices for this physical GPU to this slice.
			// This includes not-yet-manifested MIG devices, and the physical
			// GPU itself.
			slice.Devices = append(slice.Devices, device.PartGetDevice())
		}
		slice.SharedCounters = countersets
		gpuslices = append(gpuslices, slice)
	}

	return resourceslice.DriverResources{
		Pools: map[string]resourceslice.Pool{
			nodeName: {Slices: gpuslices},
		},
	}
}

func (d *driver) Shutdown() error {
	if d == nil {
		return nil
	}

	if d.healthcheck != nil {
		d.healthcheck.Stop()
	}

	// Shut down long-lived NVML session.
	if featuregates.Enabled(featuregates.DynamicMIG) {
		d.state.nvdevlib.alwaysShutdown()
	}

	if d.deviceHealthMonitor != nil {
		d.deviceHealthMonitor.Stop()
	}

	d.wg.Wait()

	if err := d.state.checkpointCleanupManager.Stop(); err != nil {
		return fmt.Errorf("error stopping CheckpointCleanupManager: %w", err)
	}

	d.pluginhelper.Stop()
	return nil
}

func (d *driver) PrepareResourceClaims(ctx context.Context, claims []*resourceapi.ResourceClaim) (map[types.UID]kubeletplugin.PrepareResult, error) {

	if len(claims) == 0 {
		// That's probably the health check, log that on higher verbosity level
		klog.V(7).Infof("PrepareResourceClaims called with %d claim(s)", len(claims))
	} else {
		// Log canonical string representation for each claim injected here --
		// we've noticed that this can greatly facilitate debugging.
		klog.V(6).Infof("Prepare called for: %v", ClaimsToStrings(claims))
	}

	results := make(map[types.UID]kubeletplugin.PrepareResult)

	for _, claim := range claims {
		results[claim.UID] = d.nodePrepareResource(ctx, claim)
	}

	return results, nil
}

func (d *driver) UnprepareResourceClaims(ctx context.Context, claimRefs []kubeletplugin.NamespacedObject) (map[types.UID]error, error) {
	klog.V(6).Infof("Unprepare called for: %v", ClaimRefsToStrings(claimRefs))
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
	// Instead of a global prepare/unprepare (PU) lock, we could rely on
	// fine-grained checkpoint locking, which was proven to work correctly in
	// case of DynamicMIG mode. However, out of caution, retain this global PU
	// lock for now in all modes (re-evaluate the performance impact at a later
	// time).
	t0 := time.Now()
	release, err := d.pulock.Acquire(ctx, flock.WithTimeout(10*time.Second))
	if err != nil {
		return kubeletplugin.PrepareResult{
			Err: fmt.Errorf("error acquiring prep/unprep lock: %w", err),
		}
	}
	defer release()
	klog.V(6).Infof("t_prep_lock_acq %.3f s", time.Since(t0).Seconds())

	cs := ResourceClaimToString(claim)
	tprep0 := time.Now()
	devs, err := d.state.Prepare(ctx, claim)
	klog.V(6).Infof("t_prep %.3f s (claim %s)", time.Since(tprep0).Seconds(), cs)

	if err != nil {
		return kubeletplugin.PrepareResult{
			Err: fmt.Errorf("error preparing devices for claim %s: %w", cs, err),
		}
	}

	if featuregates.Enabled(featuregates.PassthroughSupport) {
		// Re-advertise updated resourceslice after preparing devices.
		if err = d.publishResources(ctx, d.state.config); err != nil {
			return kubeletplugin.PrepareResult{
				Err: fmt.Errorf("error preparing devices for claim %v: %w", claim.UID, err),
			}
		}
	}

	klog.Infof("Returning newly prepared devices for claim '%s': %v", cs, devs)
	return kubeletplugin.PrepareResult{Devices: devs}
}

func (d *driver) nodeUnprepareResource(ctx context.Context, claimRef kubeletplugin.NamespacedObject) error {
	t0 := time.Now()
	release, err := d.pulock.Acquire(ctx, flock.WithTimeout(10*time.Second))
	if err != nil {
		return fmt.Errorf("error acquiring prep/unprep lock: %w", err)
	}
	defer release()
	klog.V(6).Infof("t_unprep_lock_acq %.3f s", time.Since(t0).Seconds())

	cs := claimRef.String()
	tunprep0 := time.Now()
	err = d.state.Unprepare(ctx, claimRef)
	klog.V(6).Infof("t_unprep %.3f s (claim %s)", time.Since(tunprep0).Seconds(), cs)

	if err != nil {
		return fmt.Errorf("error unpreparing devices for claim %v: %w", claimRef.String(), err)
	}

	if featuregates.Enabled(featuregates.PassthroughSupport) {
		// Re-advertise updated resourceslice after unpreparing devices.
		if err = d.publishResources(ctx, d.state.config); err != nil {
			return fmt.Errorf("error publishing resources: %w", err)
		}
	}

	return nil
}

func (d *driver) publishResources(ctx context.Context, config *Config) error {

	if featuregates.Enabled(featuregates.DynamicMIG) {
		// From KEP 4815: "we will add client-side validation in the
		// ResourceSlice controller helper, so that any errors in the
		// ResourceSlices will be caught before they even are applied to the
		// APIServer" -- the helper below is being referred to.
		//
		// TODO: implement error handler for bad slices:
		// https://github.com/kubernetes/kubernetes/commit/a171795e313ee9f407fef4897c1a1e2052120991
		klog.V(1).Infof("featuregates.DynamicMIG enabled: construct ResourceSlice objects according to KEP 4815 (partitionable devices)")
		resources := d.GenerateDriverResources(config.flags.nodeName)
		if err := d.pluginhelper.PublishResources(ctx, resources); err != nil {
			return err
		}
		return nil
	}

	// Enumerate the set of GPU, MIG and VFIO devices and publish them
	var resourceSlice resourceslice.Slice
	for _, device := range d.state.allocatable {
		klog.V(4).Infof("About to announce device %s", device.GetDevice().Name)
		resourceSlice.Devices = append(resourceSlice.Devices, device.GetDevice())
	}

	resources := resourceslice.DriverResources{
		Pools: map[string]resourceslice.Pool{
			config.flags.nodeName: {Slices: []resourceslice.Slice{resourceSlice}},
		},
	}

	if err := d.pluginhelper.PublishResources(ctx, resources); err != nil {
		return err
	}

	return nil

}

func (d *driver) deviceHealthEvents(ctx context.Context, nodeName string) {
	klog.V(4).Info("Starting to watch for device health notifications")
	for {
		select {
		case <-ctx.Done():
			klog.V(6).Info("Stop processing device health notifications")
			return
		case device, ok := <-d.deviceHealthMonitor.Unhealthy():
			if !ok {
				// NVML based deviceHealthMonitor is expected to close only during driver Shutdown.
				klog.V(6).Info("Health monitor channel closed")
				return
			}
			uuid := device.UUID()

			klog.Warningf("Received unhealthy notification for device: %s", uuid)

			if !device.IsHealthy() {
				klog.V(6).Infof("Device: %s is aleady marked unhealthy. Skip republishing ResourceSlice", uuid)
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

			klog.V(4).Info("Rebulishing resourceslice with healthy devices")
			resources := resourceslice.DriverResources{
				Pools: map[string]resourceslice.Pool{
					nodeName: {Slices: []resourceslice.Slice{resourceSlice}},
				},
			}

			// NOTE: We only log an error on publish failure and do not retry.
			// If this publish fails, our in-memory health update succeeds but the
			// ResourceSlice in the API server remains stale and still advertises the
			// now-unhealthy device as allocatable. Until a later publish succeeds,
			// the scheduler and other consumers will continue to see the unhealthy
			// device as available, and new pods may be placed onto hardware we know
			// is unusable. If publishes continue to fail (e.g., API server issues),
			// the cluster can remain in this inconsistent state indefinitely.
			// This is a temporary compromise while device taints/tolerations (KEP-5055)
			// are available as a Beta feature. An interim improvement could be adding
			// a retry/backoff or switch to patch updates instead of full republish.
			if err := d.pluginhelper.PublishResources(ctx, resources); err != nil {
				klog.Errorf("Failed to publish resources after device health status update: %v", err)
			} else {
				klog.V(4).Info("Successfully republished resources without unhealthy device")
			}
		}
	}
}

// shouldUseSplitResourceSlices detects the Kubernetes server version and
// returns true if separate ResourceSlices should be used for SharedCounters and
// Devices (required for k8s 1.35+), or false if they must be combined (k8s
// 1.34).
func shouldUseSplitResourceSlices(client coreclientset.Interface) (bool, error) {
	discoveryClient := client.Discovery()
	v, err := discoveryClient.ServerVersion()
	if err != nil {
		return false, fmt.Errorf("failed to get server version: %w", err)
	}

	shouldSplit, err := isVersion135OrLater(v)
	if err != nil {
		return false, fmt.Errorf("API server version detection failed: %w", err)
	}

	if shouldSplit {
		klog.V(2).Infof("Detected Kubernetes version %s (>= 1.35), plan to use separate ResourceSlices for SharedCounters and Devices", v.GitVersion)
		return true, nil
	}

	klog.V(2).Infof("Detected Kubernetes version %s (< 1.35), plan to use combined ResourceSlices with SharedCounters and Devices", v.GitVersion)
	return false, nil
}

func isVersion135OrLater(v *version.Info) (bool, error) {
	// `v.GitVersion`` is e.g. "v1.35.2"; semver.NewVersion handes the v prefix.
	serverVer, err := semver.NewVersion(v.GitVersion)
	if err != nil {
		return false, fmt.Errorf("failed to parse version '%s': %w", v.GitVersion, err)
	}

	minVersion := semver.MustParse("1.35.0")
	return serverVer.GreaterThan(minVersion) || serverVer.Equal(minVersion), nil
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
