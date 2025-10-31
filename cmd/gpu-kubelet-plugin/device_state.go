/*
 * Copyright (c) 2022-2025, NVIDIA CORPORATION.  All rights reserved.
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
	"slices"
	"sync"

	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"
	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"

	configapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
)

type OpaqueDeviceConfig struct {
	Requests []string
	Config   runtime.Object
}

type DeviceConfigState struct {
	MpsControlDaemonID string `json:"mpsControlDaemonID"`
	containerEdits     *cdiapi.ContainerEdits
}

type DeviceState struct {
	sync.Mutex
	cdi            *CDIHandler
	tsManager      *TimeSlicingManager
	mpsManager     *MpsManager
	vfioPciManager *VfioPciManager
	allocatable    AllocatableDevices
	config         *Config

	nvdevlib          *deviceLib
	checkpointManager checkpointmanager.CheckpointManager
}

func NewDeviceState(ctx context.Context, config *Config) (*DeviceState, error) {
	containerDriverRoot := root(config.flags.containerDriverRoot)
	nvdevlib, err := newDeviceLib(containerDriverRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to create device library: %w", err)
	}

	allocatable, err := nvdevlib.enumerateAllPossibleDevices(config)
	if err != nil {
		return nil, fmt.Errorf("error enumerating all possible devices: %w", err)
	}

	devRoot := containerDriverRoot.getDevRoot()
	klog.Infof("using devRoot=%v", devRoot)

	hostDriverRoot := config.flags.hostDriverRoot
	cdi, err := NewCDIHandler(
		WithNvml(nvdevlib.nvmllib),
		WithDeviceLib(nvdevlib),
		WithDriverRoot(string(containerDriverRoot)),
		WithDevRoot(devRoot),
		WithTargetDriverRoot(hostDriverRoot),
		WithNVIDIACDIHookPath(config.flags.nvidiaCDIHookPath),
		WithCDIRoot(config.flags.cdiRoot),
		WithVendor(cdiVendor),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create CDI handler: %w", err)
	}

	var tsManager *TimeSlicingManager
	if featuregates.Enabled(featuregates.TimeSlicingSettings) {
		tsManager = NewTimeSlicingManager(nvdevlib)
	}

	var mpsManager *MpsManager
	if featuregates.Enabled(featuregates.MPSSupport) {
		mpsManager = NewMpsManager(config, nvdevlib, hostDriverRoot, MpsControlDaemonTemplatePath)
	}

	var vfioPciManager *VfioPciManager
	if featuregates.Enabled(featuregates.PassthroughSupport) {
		manager := NewVfioPciManager(string(containerDriverRoot), string(hostDriverRoot), nvdevlib, true /* nvidiaEnabled */)
		if err := manager.Prechecks(); err == nil {
			vfioPciManager = manager
		} else {
			klog.Warningf("vfio-pci manager failed prechecks, will not be initialize: %v", err)
		}
	}

	if err := cdi.CreateStandardDeviceSpecFile(allocatable); err != nil {
		return nil, fmt.Errorf("unable to create base CDI spec file: %v", err)
	}

	checkpointManager, err := checkpointmanager.NewCheckpointManager(config.DriverPluginPath())
	if err != nil {
		return nil, fmt.Errorf("unable to create checkpoint manager: %v", err)
	}

	state := &DeviceState{
		cdi:               cdi,
		tsManager:         tsManager,
		mpsManager:        mpsManager,
		vfioPciManager:    vfioPciManager,
		config:            config,
		nvdevlib:          nvdevlib,
		checkpointManager: checkpointManager,
	}
	state.allocatable = allocatable

	checkpoints, err := state.checkpointManager.ListCheckpoints()
	if err != nil {
		return nil, fmt.Errorf("unable to list checkpoints: %v", err)
	}

	for _, c := range checkpoints {
		if c == DriverPluginCheckpointFileBasename {
			return state, nil
		}
	}

	if err := state.createCheckpoint(&Checkpoint{}); err != nil {
		return nil, fmt.Errorf("unable to create checkpoint: %v", err)
	}

	return state, nil
}

func (s *DeviceState) Prepare(ctx context.Context, claim *resourceapi.ResourceClaim) ([]kubeletplugin.Device, error) {
	s.Lock()
	defer s.Unlock()

	claimUID := string(claim.UID)

	checkpoint, err := s.getCheckpoint()
	if err != nil {
		return nil, fmt.Errorf("unable to get checkpoint: %v", err)
	}

	err = s.updateCheckpoint(func(checkpoint *Checkpoint) {
		checkpoint.V2.PreparedClaims[claimUID] = PreparedClaim{
			CheckpointState: ClaimCheckpointStatePrepareStarted,
			Status:          claim.Status,
		}
	})
	if err != nil {
		return nil, fmt.Errorf("unable to update checkpoint: %w", err)
	}
	klog.V(6).Infof("checkpoint updated for claim %v", claimUID)

	preparedClaim, exists := checkpoint.V2.PreparedClaims[claimUID]
	if exists && preparedClaim.CheckpointState == ClaimCheckpointStatePrepareCompleted {
		// Make this a noop. Associated device(s) has/ave been prepared by us.
		// Prepare() must be idempotent, as it may be invoked more than once per
		// claim (and actual device preparation must happen at most once).
		klog.V(6).Infof("skip prepare: claim %v found in checkpoint", claimUID)
		return preparedClaim.PreparedDevices.GetDevices(), nil
	}

	preparedDevices, err := s.prepareDevices(ctx, claim)
	if err != nil {
		return nil, fmt.Errorf("prepare devices failed: %w", err)
	}

	if featuregates.Enabled(featuregates.PassthroughSupport) {
		for _, device := range preparedDevices.GetDevices() {
			allocatableDevice, ok := s.allocatable[device.DeviceName]
			if !ok {
				klog.Warningf("allocatable not found for device: %v", device.DeviceName)
				continue
			}
			s.allocatable.RemoveSiblingDevices(allocatableDevice)
		}
	}

	if err := s.cdi.CreateClaimSpecFile(claimUID, preparedDevices); err != nil {
		return nil, fmt.Errorf("unable to create CDI spec file for claim: %w", err)
	}

	err = s.updateCheckpoint(func(checkpoint *Checkpoint) {
		checkpoint.V2.PreparedClaims[claimUID] = PreparedClaim{
			CheckpointState: ClaimCheckpointStatePrepareCompleted,
			Status:          claim.Status,
			PreparedDevices: preparedDevices,
		}
	})
	if err != nil {
		return nil, fmt.Errorf("unable to update checkpoint: %w", err)
	}
	klog.V(6).Infof("checkpoint updated for claim %v", claimUID)

	return preparedDevices.GetDevices(), nil
}

func (s *DeviceState) Unprepare(ctx context.Context, claimUID string) error {
	s.Lock()
	defer s.Unlock()

	checkpoint, err := s.getCheckpoint()
	if err != nil {
		return fmt.Errorf("unable to get checkpoint: %v", err)
	}

	pc, exists := checkpoint.V2.PreparedClaims[claimUID]
	if !exists {
		// Not an error: if this claim UID is not in the checkpoint then this
		// device was never prepared or has already been unprepared (assume that
		// Prepare+Checkpoint are done transactionally). Note that
		// claimRef.String() contains namespace, name, UID.
		klog.Infof("unprepare noop: claim not found in checkpoint data: %v", claimUID)
		return nil
	}

	switch pc.CheckpointState {
	case ClaimCheckpointStatePrepareStarted:
		klog.Infof("unprepare noop: claim preparation started but not completed: %v", claimUID)
		return nil
	case ClaimCheckpointStatePrepareCompleted:
		if err := s.unprepareDevices(ctx, claimUID, pc.PreparedDevices); err != nil {
			return fmt.Errorf("unprepare devices failed: %w", err)
		}
	default:
		return fmt.Errorf("unsupported ClaimCheckpointState: %v", pc.CheckpointState)
	}

	if featuregates.Enabled(featuregates.PassthroughSupport) {
		for _, device := range pc.PreparedDevices.GetDevices() {
			allocatableDevice, ok := s.allocatable[device.DeviceName]
			if !ok {
				klog.Warningf("allocatable not found for device: %v", device.DeviceName)
				continue
			}
			err := s.discoverSiblingAllocatables(allocatableDevice)
			if err != nil {
				return fmt.Errorf("error discovering sibling allocatables: %w", err)
			}
		}
	}
	if err := s.cdi.DeleteClaimSpecFile(claimUID); err != nil {
		return fmt.Errorf("unable to delete CDI spec file for claim: %w", err)
	}

	// Unprepare succeeded; reflect that in the node-local checkpoint data.
	delete(checkpoint.V2.PreparedClaims, claimUID)
	if err := s.checkpointManager.CreateCheckpoint(DriverPluginCheckpointFileBasename, checkpoint); err != nil {
		return fmt.Errorf("unable to sync to checkpoint: %v", err)
	}

	return nil
}

func (s *DeviceState) createCheckpoint(cp *Checkpoint) error {
	return s.checkpointManager.CreateCheckpoint(DriverPluginCheckpointFileBasename, cp)
}

func (s *DeviceState) getCheckpoint() (*Checkpoint, error) {
	checkpoint := &Checkpoint{}
	if err := s.checkpointManager.GetCheckpoint(DriverPluginCheckpointFileBasename, checkpoint); err != nil {
		return nil, err
	}
	return checkpoint.ToLatestVersion(), nil
}

func (s *DeviceState) updateCheckpoint(f func(*Checkpoint)) error {
	checkpoint, err := s.getCheckpoint()
	if err != nil {
		return fmt.Errorf("unable to get checkpoint: %w", err)
	}

	f(checkpoint)

	if err := s.checkpointManager.CreateCheckpoint(DriverPluginCheckpointFileBasename, checkpoint); err != nil {
		return fmt.Errorf("unable to create checkpoint: %w", err)
	}

	return nil
}

func (s *DeviceState) prepareDevices(ctx context.Context, claim *resourceapi.ResourceClaim) (PreparedDevices, error) {
	if claim.Status.Allocation == nil {
		return nil, fmt.Errorf("claim not yet allocated")
	}

	// Retrieve the full set of device configs for the driver.
	configs, err := GetOpaqueDeviceConfigs(
		configapi.StrictDecoder,
		DriverName,
		claim.Status.Allocation.Devices.Config,
	)
	if err != nil {
		return nil, fmt.Errorf("error getting opaque device configs: %v", err)
	}

	// Add the default GPU and MIG device Configs to the front of the config
	// list with the lowest precedence. This guarantees there will be at least
	// one of each config in the list with len(Requests) == 0 for the lookup below.
	configs = slices.Insert(configs, 0, &OpaqueDeviceConfig{
		Requests: []string{},
		Config:   configapi.DefaultGpuConfig(),
	})
	configs = slices.Insert(configs, 0, &OpaqueDeviceConfig{
		Requests: []string{},
		Config:   configapi.DefaultMigDeviceConfig(),
	})
	if featuregates.Enabled(featuregates.PassthroughSupport) {
		configs = slices.Insert(configs, 0, &OpaqueDeviceConfig{
			Requests: []string{},
			Config:   configapi.DefaultVfioDeviceConfig(),
		})
	}

	// Look through the configs and figure out which one will be applied to
	// each device allocation result based on their order of precedence and type.
	configResultsMap := make(map[runtime.Object][]*resourceapi.DeviceRequestAllocationResult)
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver != DriverName {
			continue
		}
		device, exists := s.allocatable[result.Device]
		if !exists {
			return nil, fmt.Errorf("requested device is not allocatable: %v", result.Device)
		}
		for _, c := range slices.Backward(configs) {
			if slices.Contains(c.Requests, result.Request) {
				if _, ok := c.Config.(*configapi.GpuConfig); ok && device.Type() != GpuDeviceType {
					return nil, fmt.Errorf("cannot apply GPU config to request: %v", result.Request)
				}
				if _, ok := c.Config.(*configapi.MigDeviceConfig); ok && device.Type() != MigDeviceType {
					return nil, fmt.Errorf("cannot apply MIG device config to request: %v", result.Request)
				}
				if _, ok := c.Config.(*configapi.VfioDeviceConfig); ok && device.Type() != VfioDeviceType {
					return nil, fmt.Errorf("cannot apply VFIO device config to request: %v", result.Request)
				}
				configResultsMap[c.Config] = append(configResultsMap[c.Config], &result)
				break
			}
			if len(c.Requests) == 0 {
				if _, ok := c.Config.(*configapi.GpuConfig); ok && device.Type() != GpuDeviceType {
					continue
				}
				if _, ok := c.Config.(*configapi.MigDeviceConfig); ok && device.Type() != MigDeviceType {
					continue
				}
				if _, ok := c.Config.(*configapi.VfioDeviceConfig); ok && device.Type() != VfioDeviceType {
					continue
				}
				configResultsMap[c.Config] = append(configResultsMap[c.Config], &result)
				break
			}
		}
	}

	// Normalize, validate, and apply all configs associated with devices that
	// need to be prepared. Track device group configs generated from applying the
	// config to the set of device allocation results.
	preparedDeviceGroupConfigState := make(map[runtime.Object]*DeviceConfigState)
	for c, results := range configResultsMap {
		// Cast the opaque config to a configapi.Interface type
		var config configapi.Interface
		switch castConfig := c.(type) {
		case *configapi.GpuConfig:
			config = castConfig
		case *configapi.MigDeviceConfig:
			config = castConfig
		case *configapi.VfioDeviceConfig:
			config = castConfig
		default:
			return nil, fmt.Errorf("runtime object is not a recognized configuration")
		}

		// Normalize the config to set any implied defaults.
		if err := config.Normalize(); err != nil {
			return nil, fmt.Errorf("error normalizing GPU config: %w", err)
		}

		// Validate the config to ensure its integrity.
		if err := config.Validate(); err != nil {
			return nil, fmt.Errorf("error validating GPU config: %w", err)
		}

		// Apply the config to the list of results associated with it.
		configState, err := s.applyConfig(ctx, config, claim, results)
		if err != nil {
			return nil, fmt.Errorf("error applying GPU config: %w", err)
		}

		// Capture the prepared device group config in the map.
		preparedDeviceGroupConfigState[c] = configState
	}

	// Walk through each config and its associated device allocation results
	// and construct the list of prepared devices to return.
	var preparedDevices PreparedDevices
	for c, results := range configResultsMap {
		preparedDeviceGroup := PreparedDeviceGroup{
			ConfigState: *preparedDeviceGroupConfigState[c],
		}

		for _, result := range results {
			cdiDevices := []string{}
			if d := s.cdi.GetStandardDevice(s.allocatable[result.Device]); d != "" {
				cdiDevices = append(cdiDevices, d)
			}
			if d := s.cdi.GetClaimDevice(string(claim.UID), s.allocatable[result.Device], preparedDeviceGroupConfigState[c].containerEdits); d != "" {
				cdiDevices = append(cdiDevices, d)
			}

			device := &kubeletplugin.Device{
				Requests:     []string{result.Request},
				PoolName:     result.Pool,
				DeviceName:   result.Device,
				CDIDeviceIDs: cdiDevices,
			}

			var preparedDevice PreparedDevice
			switch s.allocatable[result.Device].Type() {
			case GpuDeviceType:
				preparedDevice.Gpu = &PreparedGpu{
					Info:   s.allocatable[result.Device].Gpu,
					Device: device,
				}
			case MigDeviceType:
				preparedDevice.Mig = &PreparedMigDevice{
					Info:   s.allocatable[result.Device].Mig,
					Device: device,
				}
			case VfioDeviceType:
				preparedDevice.Vfio = &PreparedVfioDevice{
					Info:   s.allocatable[result.Device].Vfio,
					Device: device,
				}
			}

			preparedDeviceGroup.Devices = append(preparedDeviceGroup.Devices, preparedDevice)
		}

		preparedDevices = append(preparedDevices, &preparedDeviceGroup)
	}
	return preparedDevices, nil
}

func (s *DeviceState) unprepareDevices(ctx context.Context, claimUID string, devices PreparedDevices) error {
	for _, group := range devices {
		// Unconfigure the vfio-pci devices.
		if featuregates.Enabled(featuregates.PassthroughSupport) {
			err := s.unprepareVfioDevices(ctx, group.Devices.VfioDevices())
			if err != nil {
				return err
			}
		}

		// Stop any MPS control daemons started for each group of prepared devices.
		if featuregates.Enabled(featuregates.MPSSupport) {
			mpsControlDaemon := s.mpsManager.NewMpsControlDaemon(claimUID, group)
			if err := mpsControlDaemon.Stop(ctx); err != nil {
				return fmt.Errorf("error stopping MPS control daemon: %w", err)
			}
		}

		// Go back to default time-slicing for all full GPUs.
		if featuregates.Enabled(featuregates.TimeSlicingSettings) {
			tsc := configapi.DefaultGpuConfig().Sharing.TimeSlicingConfig
			if err := s.tsManager.SetTimeSlice(group.Devices.Gpus(), tsc); err != nil {
				return fmt.Errorf("error setting timeslice for devices: %w", err)
			}
		}

	}
	return nil
}

func (s *DeviceState) getAllocatableVfioDevice(uuid string) (*AllocatableDevice, error) {
	for _, allocatable := range s.allocatable {
		if allocatable.Type() != VfioDeviceType {
			continue
		}
		if allocatable.Vfio.UUID == uuid {
			return allocatable, nil
		}
	}
	return nil, fmt.Errorf("allocatable device not found for vfio device: %v", uuid)
}

func (s *DeviceState) unprepareVfioDevices(ctx context.Context, devices PreparedDeviceList) error {
	for _, device := range devices {
		vfioAllocatable, err := s.getAllocatableVfioDevice(device.Vfio.Info.UUID)
		if err != nil {
			return fmt.Errorf("error getting allocatable device for vfio device: %w", err)
		}
		if err := s.vfioPciManager.Unconfigure(ctx, vfioAllocatable.Vfio); err != nil {
			return fmt.Errorf("error unconfiguring vfio device: %w", err)
		}
	}
	return nil
}

func (s *DeviceState) discoverSiblingAllocatables(device *AllocatableDevice) error {
	switch device.Type() {
	case GpuDeviceType:
		if !device.Gpu.vfioEnabled {
			return nil
		}
		vfio, err := s.nvdevlib.discoverVfioDevice(device.Gpu)
		if err != nil {
			return fmt.Errorf("error discovering vfio device: %w", err)
		}
		s.allocatable[vfio.CanonicalName()] = vfio
	case VfioDeviceType:
		gpu, migs, err := s.nvdevlib.discoverGPUByPCIBusID(device.Vfio.pcieBusID)
		if err != nil {
			return fmt.Errorf("error discovering gpu by pci bus id: %w", err)
		}
		s.allocatable[gpu.CanonicalName()] = gpu
		device.Vfio.parent = gpu.Gpu
		for _, mig := range migs {
			s.allocatable[mig.CanonicalName()] = mig
		}
	case MigDeviceType:
		// TODO: Implement once dynamic MIG is supported.
		return nil
	}
	return nil
}

func (s *DeviceState) applyConfig(ctx context.Context, config configapi.Interface, claim *resourceapi.ResourceClaim, results []*resourceapi.DeviceRequestAllocationResult) (*DeviceConfigState, error) {
	switch castConfig := config.(type) {
	case *configapi.GpuConfig:
		return s.applySharingConfig(ctx, castConfig.Sharing, claim, results)
	case *configapi.MigDeviceConfig:
		return s.applySharingConfig(ctx, castConfig.Sharing, claim, results)
	case *configapi.VfioDeviceConfig:
		return s.applyVfioDeviceConfig(ctx, castConfig, claim, results)
	default:
		return nil, fmt.Errorf("unknown config type: %T", castConfig)
	}
}

func (s *DeviceState) applySharingConfig(ctx context.Context, config configapi.Sharing, claim *resourceapi.ResourceClaim, results []*resourceapi.DeviceRequestAllocationResult) (*DeviceConfigState, error) {
	// Get the list of claim requests this config is being applied over.
	var requests []string
	for _, r := range results {
		requests = append(requests, r.Request)
	}

	// Get the list of allocatable devices this config is being applied over.
	allocatableDevices := make(AllocatableDevices)
	for _, r := range results {
		allocatableDevices[r.Device] = s.allocatable[r.Device]
	}

	// Declare a device group state object to populate.
	var configState DeviceConfigState

	// Apply time-slicing settings (if available and feature gate enabled).
	if featuregates.Enabled(featuregates.TimeSlicingSettings) && config.IsTimeSlicing() {
		tsc, err := config.GetTimeSlicingConfig()
		if err != nil {
			return nil, fmt.Errorf("error getting timeslice config for requests '%v' in claim '%v': %w", requests, claim.UID, err)
		}
		if tsc != nil {
			err = s.tsManager.SetTimeSlice(allocatableDevices, tsc)
			if err != nil {
				return nil, fmt.Errorf("error setting timeslice config for requests '%v' in claim '%v': %w", requests, claim.UID, err)
			}
		}
	}

	// Apply MPS settings (if available and feature gate enabled).
	if featuregates.Enabled(featuregates.MPSSupport) && config.IsMps() {
		mpsc, err := config.GetMpsConfig()
		if err != nil {
			return nil, fmt.Errorf("error getting MPS configuration: %w", err)
		}
		mpsControlDaemon := s.mpsManager.NewMpsControlDaemon(string(claim.UID), allocatableDevices)
		if err := mpsControlDaemon.Start(ctx, mpsc); err != nil {
			return nil, fmt.Errorf("error starting MPS control daemon: %w", err)
		}
		if err := mpsControlDaemon.AssertReady(ctx); err != nil {
			return nil, fmt.Errorf("MPS control daemon is not yet ready: %w", err)
		}
		configState.MpsControlDaemonID = mpsControlDaemon.GetID()
		configState.containerEdits = mpsControlDaemon.GetCDIContainerEdits()
	}

	return &configState, nil
}

func (s *DeviceState) applyVfioDeviceConfig(ctx context.Context, config *configapi.VfioDeviceConfig, claim *resourceapi.ResourceClaim, results []*resourceapi.DeviceRequestAllocationResult) (*DeviceConfigState, error) {
	if !featuregates.Enabled(featuregates.PassthroughSupport) {
		return nil, nil
	}
	var configState DeviceConfigState

	// Configure the vfio-pci devices.
	for _, r := range results {
		info := s.allocatable[r.Device]
		err := s.vfioPciManager.Configure(ctx, info.Vfio)
		if err != nil {
			return nil, err
		}
	}

	return &configState, nil
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
	possibleConfigs []resourceapi.DeviceAllocationConfiguration,
) ([]*OpaqueDeviceConfig, error) {
	// Collect all configs in order of reverse precedence.
	var classConfigs []resourceapi.DeviceAllocationConfiguration
	var claimConfigs []resourceapi.DeviceAllocationConfiguration
	var candidateConfigs []resourceapi.DeviceAllocationConfiguration
	for _, config := range possibleConfigs {
		switch config.Source {
		case resourceapi.AllocationConfigSourceClass:
			classConfigs = append(classConfigs, config)
		case resourceapi.AllocationConfigSourceClaim:
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

// TODO: Dynamic MIG is not yet supported with structured parameters.
// Refactor this to allow for the allocation of statically partitioned MIG
// devices.
//
// func (s *DeviceState) prepareMigDevices(claimUID string, allocated *nascrd.AllocatedMigDevices) (*PreparedMigDevices, error) {
// 	prepared := &PreparedMigDevices{}
//
// 	for _, device := range allocated.Devices {
// 		if _, exists := s.allocatable[device.ParentUUID]; !exists {
// 			return nil, fmt.Errorf("allocated GPU does not exist: %v", device.ParentUUID)
// 		}
//
// 		parent := s.allocatable[device.ParentUUID]
//
// 		if !parent.migEnabled {
// 			return nil, fmt.Errorf("cannot prepare a GPU with MIG mode disabled: %v", device.ParentUUID)
// 		}
//
// 		if _, exists := parent.migProfiles[device.Profile]; !exists {
// 			return nil, fmt.Errorf("MIG profile %v does not exist on GPU: %v", device.Profile, device.ParentUUID)
// 		}
//
// 		placement := nvml.GpuInstancePlacement{
// 			Start: uint32(device.Placement.Start),
// 			Size:  uint32(device.Placement.Size),
// 		}
//
// 		migInfo, err := s.nvdevlib.createMigDevice(parent.GpuInfo, parent.migProfiles[device.Profile].profile, &placement)
// 		if err != nil {
// 			return nil, fmt.Errorf("error creating MIG device: %w", err)
// 		}
//
// 		prepared.Devices = append(prepared.Devices, migInfo)
// 	}
//
// 	return prepared, nil
// }
//
// func (s *DeviceState) unprepareMigDevices(claimUID string, devices *PreparedDevices) error {
// 	for _, device := range devices.Mig.Devices {
// 		err := s.nvdevlib.deleteMigDevice(device)
// 		if err != nil {
// 			return fmt.Errorf("error deleting MIG device for %v: %w", device.uuid, err)
// 		}
// 	}
// 	return nil
//}
