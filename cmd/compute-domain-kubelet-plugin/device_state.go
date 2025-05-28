/*
 * Copyright (c) 2022, NVIDIA CORPORATION.  All rights reserved.
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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager"
	cperrors "k8s.io/kubernetes/pkg/kubelet/checkpointmanager/errors"
	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"

	configapi "github.com/NVIDIA/k8s-dra-driver-gpu/api/nvidia.com/resource/v1beta1"
)

type OpaqueDeviceConfig struct {
	Requests []string
	Config   runtime.Object
}

type DeviceConfigState struct {
	Type           string
	ComputeDomain  string
	containerEdits *cdiapi.ContainerEdits
}

type DeviceState struct {
	sync.Mutex
	cdi                  *CDIHandler
	computeDomainManager *ComputeDomainManager
	allocatable          AllocatableDevices
	config               *Config

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
		WithNvidiaCTKPath(config.flags.nvidiaCTKPath),
		WithCDIRoot(config.flags.cdiRoot),
		WithVendor(cdiVendor),
	)
	if err != nil {
		return nil, fmt.Errorf("unable to create CDI handler: %w", err)
	}

	cliqueID, err := nvdevlib.getCliqueID()
	if err != nil {
		return nil, fmt.Errorf("error getting cliqueID: %w", err)
	}

	computeDomainManager := NewComputeDomainManager(config, ComputeDomainDaemonSettingsRoot, cliqueID)

	if err := cdi.CreateStandardDeviceSpecFile(allocatable); err != nil {
		return nil, fmt.Errorf("unable to create base CDI spec file: %v", err)
	}

	checkpointManager, err := checkpointmanager.NewCheckpointManager(DriverPluginPath)
	if err != nil {
		return nil, fmt.Errorf("unable to create checkpoint manager: %v", err)
	}

	state := &DeviceState{
		cdi:                  cdi,
		computeDomainManager: computeDomainManager,
		allocatable:          allocatable,
		config:               config,
		nvdevlib:             nvdevlib,
		checkpointManager:    checkpointManager,
	}

	checkpoints, err := state.checkpointManager.ListCheckpoints()
	if err != nil {
		return nil, fmt.Errorf("unable to list checkpoints: %v", err)
	}

	for _, c := range checkpoints {
		if c == DriverPluginCheckpointFileBasename {
			return state, nil
		}
	}

	checkpoint := newCheckpoint()
	if err := state.checkpointManager.CreateCheckpoint(DriverPluginCheckpointFileBasename, checkpoint); err != nil {
		return nil, fmt.Errorf("unable to sync to checkpoint: %v", err)
	}

	return state, nil
}

func (s *DeviceState) Prepare(ctx context.Context, claim *resourceapi.ResourceClaim) ([]kubeletplugin.Device, error) {
	s.Lock()
	defer s.Unlock()

	claimUID := string(claim.UID)

	checkpoint := newCheckpoint()
	if err := s.checkpointManager.GetCheckpoint(DriverPluginCheckpointFileBasename, checkpoint); err != nil {
		return nil, fmt.Errorf("unable to get checkpoint: %w", err)
	}

	preparedClaim, exists := checkpoint.V1.PreparedClaims[claimUID]
	if exists {
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

	if err := s.cdi.CreateClaimSpecFile(claimUID, preparedDevices); err != nil {
		return nil, fmt.Errorf("unable to create CDI spec file for claim: %w", err)
	}

	// Add ResourceClaimStatus API object to node-local checkpoint: the
	// 'unprepare' code path must use local state exclusively (ResourceClaim
	// object might have been deleted from the API server).
	checkpoint.V1.PreparedClaims[claimUID] = PreparedClaim{
		Status:          claim.Status,
		PreparedDevices: preparedDevices,
	}
	if err := s.checkpointManager.CreateCheckpoint(DriverPluginCheckpointFileBasename, checkpoint); err != nil {
		return nil, fmt.Errorf("unable to create checkpoint: %w", err)
	}
	klog.V(6).Infof("checkpoint written for claim %v", claimUID)

	return preparedDevices.GetDevices(), nil
}

func (s *DeviceState) Unprepare(ctx context.Context, claimRef kubeletplugin.NamespacedObject) error {
	s.Lock()
	defer s.Unlock()

	claimUID := string(claimRef.UID)

	// Rely on local checkpoint state for ability to clean up.
	checkpoint := newCheckpoint()
	if err := s.checkpointManager.GetCheckpoint(DriverPluginCheckpointFileBasename, checkpoint); err != nil {
		// Some errors returned by `GetCheckpoint` are permanent. Some of them
		// may be retryable. For now, handle an explicit set of errors as
		// permanent, and treat every other error as retryable.

		if errors.Is(err, &json.UnmarshalTypeError{}) {
			// May for example happen when a different version of this program
			// wrote the JSON doc (using a different JSON schema).
			return permanentError{fmt.Errorf("get checkpoint: unexpected schema (treat permanent): %w", err)}
		}

		if errors.Is(err, &cperrors.CorruptCheckpointError{}) {
			return permanentError{fmt.Errorf("get checkpoint: bad checksum (treat permanent): %w", err)}
		}

		return fmt.Errorf("unable to get checkpoint: %w", err)
	}

	pc, exists := checkpoint.V1.PreparedClaims[claimUID]
	if !exists {
		// Not an error: if this claim UID is not in the checkpoint then this
		// device was never prepared or has already been unprepared (assume that
		// Prepare+Checkpoint are done transactionally). Note that
		// claimRef.String() contains namespace, name, UID.
		klog.Infof("unprepare noop: claim not found in checkpoint data: %v", claimRef.String())
		return nil
	}

	if err := s.unprepareDevices(ctx, &pc.Status); err != nil {
		return fmt.Errorf("unprepare devices failed: %w", err)
	}

	err := s.cdi.DeleteClaimSpecFile(claimUID)
	if err != nil {
		return fmt.Errorf("unable to delete CDI spec file for claim: %w", err)
	}

	// Write new checkpoint reflecting that all devices for this claim have been
	// unprepared (by virtue of removing its UID from all mappings).
	delete(checkpoint.V1.PreparedClaims, claimUID)
	if err := s.checkpointManager.CreateCheckpoint(DriverPluginCheckpointFileBasename, checkpoint); err != nil {
		return fmt.Errorf("create checkpoint failed: %w", err)
	}

	return nil
}

func (s *DeviceState) prepareDevices(ctx context.Context, claim *resourceapi.ResourceClaim) (PreparedDevices, error) {
	// Generate a mapping of each OpaqueDeviceConfigs to the Device.Results it applies to
	configResultsMap, err := s.getConfigResultsMap(&claim.Status)
	if err != nil {
		return nil, fmt.Errorf("error generating configResultsMap: %w", err)
	}

	// Normalize, validate, and apply all configs associated with devices that
	// need to be prepared. Track device group configs generated from applying the
	// config to the set of device allocation results.
	preparedDeviceGroupConfigState := make(map[runtime.Object]*DeviceConfigState)
	for c, results := range configResultsMap {
		// Cast the opaque config to a configapi.Interface type
		var config configapi.Interface
		switch castConfig := c.(type) {
		case *configapi.ComputeDomainChannelConfig:
			config = castConfig
		case *configapi.ComputeDomainDaemonConfig:
			config = castConfig
		default:
			return nil, fmt.Errorf("runtime object is not a recognized configuration")
		}

		// Normalize the config to set any implied defaults.
		if err := config.Normalize(); err != nil {
			return nil, fmt.Errorf("error normalizing config: %w", err)
		}

		// Validate the config to ensure its integrity.
		if err := config.Validate(); err != nil {
			return nil, fmt.Errorf("error validating config: %w", err)
		}

		// Apply the config to the list of results associated with it.
		configState, err := s.applyConfig(ctx, config, claim, results)
		if err != nil {
			return nil, fmt.Errorf("error applying config: %w", err)
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

			device := kubeletplugin.Device{
				Requests:     []string{result.Request},
				PoolName:     result.Pool,
				DeviceName:   result.Device,
				CDIDeviceIDs: cdiDevices,
			}

			var preparedDevice PreparedDevice
			switch s.allocatable[result.Device].Type() {
			case ComputeDomainChannelType:
				preparedDevice.Channel = &PreparedComputeDomainChannel{
					Info:   s.allocatable[result.Device].Channel,
					Device: &device,
				}
			case ComputeDomainDaemonType:
				preparedDevice.Daemon = &PreparedComputeDomainDaemon{
					Info:   s.allocatable[result.Device].Daemon,
					Device: &device,
				}
			}

			preparedDeviceGroup.Devices = append(preparedDeviceGroup.Devices, preparedDevice)
		}

		preparedDevices = append(preparedDevices, &preparedDeviceGroup)
	}
	return preparedDevices, nil
}

func (s *DeviceState) unprepareDevices(ctx context.Context, cs *resourceapi.ResourceClaimStatus) error {
	// Generate a mapping of each OpaqueDeviceConfigs to the Device.Results it applies to
	configResultsMap, err := s.getConfigResultsMap(cs)
	if err != nil {
		return fmt.Errorf("error generating configResultsMap: %w", err)
	}

	// Unprepare any ComputeDomain daemons prepared for each group of prepared devices.
	for c := range configResultsMap {
		switch config := c.(type) {
		case *configapi.ComputeDomainChannelConfig:
			// If a channel type, remove the ComputeDomain label from the node
			if err := s.computeDomainManager.RemoveNodeLabel(ctx, config.DomainID); err != nil {
				return fmt.Errorf("error removing Node label for ComputeDomain: %w", err)
			}
		case *configapi.ComputeDomainDaemonConfig:
			// If a daemon type, unprepare the new ComputeDomain daemon.
			computeDomainDaemonSettings := s.computeDomainManager.NewSettings(config.DomainID)
			if err := computeDomainDaemonSettings.Unprepare(ctx); err != nil {
				return fmt.Errorf("error unpreparing ComputeDomain daemon settings: %w", err)
			}
		}
	}

	return nil
}

func (s *DeviceState) applyConfig(ctx context.Context, config configapi.Interface, claim *resourceapi.ResourceClaim, results []*resourceapi.DeviceRequestAllocationResult) (*DeviceConfigState, error) {
	switch castConfig := config.(type) {
	case *configapi.ComputeDomainChannelConfig:
		return s.applyComputeDomainChannelConfig(ctx, castConfig, claim, results)
	case *configapi.ComputeDomainDaemonConfig:
		return s.applyComputeDomainDaemonConfig(ctx, castConfig, claim, results)
	default:
		return nil, fmt.Errorf("unknown config type: %T", castConfig)
	}
}

func (s *DeviceState) applyComputeDomainChannelConfig(ctx context.Context, config *configapi.ComputeDomainChannelConfig, claim *resourceapi.ResourceClaim, results []*resourceapi.DeviceRequestAllocationResult) (*DeviceConfigState, error) {
	// Declare a device group state object to populate.
	configState := DeviceConfigState{
		Type:          ComputeDomainChannelType,
		ComputeDomain: config.DomainID,
	}

	// Create any necessary ComputeDomain channels and gather their CDI container edits.
	for _, r := range results {
		channel := s.allocatable[r.Device].Channel
		if err := s.computeDomainManager.AssertComputeDomainNamespace(ctx, claim.Namespace, config.DomainID); err != nil {
			return nil, permanentError{fmt.Errorf("error asserting ComputeDomain's namespace: %w", err)}
		}
		if err := s.computeDomainManager.AddNodeLabel(ctx, config.DomainID); err != nil {
			return nil, fmt.Errorf("error adding Node label for ComputeDomain: %w", err)
		}
		if err := s.computeDomainManager.AssertComputeDomainReady(ctx, config.DomainID); err != nil {
			return nil, fmt.Errorf("error asserting ComputeDomain Ready: %w", err)
		}
		if s.computeDomainManager.cliqueID != "" {
			if err := s.nvdevlib.createComputeDomainChannelDevice(channel.ID); err != nil {
				return nil, fmt.Errorf("error creating ComputeDomain channel device: %w", err)
			}
			configState.containerEdits = configState.containerEdits.Append(s.computeDomainManager.GetComputeDomainChannelContainerEdits(s.cdi.devRoot, channel))
		}
	}

	return &configState, nil
}

func (s *DeviceState) applyComputeDomainDaemonConfig(ctx context.Context, config *configapi.ComputeDomainDaemonConfig, claim *resourceapi.ResourceClaim, results []*resourceapi.DeviceRequestAllocationResult) (*DeviceConfigState, error) {
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

	if len(allocatableDevices) != 1 {
		return nil, fmt.Errorf("only expected 1 device for requests '%v' in claim '%v'", requests, claim.UID)
	}

	// Add info about this node to the ComputeDomain status.
	if err := s.computeDomainManager.AddNodeStatusToComputeDomain(ctx, config.DomainID); err != nil {
		return nil, fmt.Errorf("error adding node status to ComputeDomain: %w", err)
	}

	// Declare a device group state object to populate.
	configState := DeviceConfigState{
		Type:          ComputeDomainDaemonType,
		ComputeDomain: config.DomainID,
	}

	// Only prepare files to inject to the daemon if IMEX is supported.
	if s.computeDomainManager.cliqueID != "" {
		// Parse the device node info for the fabic-imex-mgmt nvcap.
		nvcapDeviceInfo, err := s.nvdevlib.parseNVCapDeviceInfo(nvidiaCapFabricImexMgmtPath)
		if err != nil {
			return nil, fmt.Errorf("error parsing nvcap device info for fabic-imex-mgmt: %w", err)
		}

		// Create the device node for the fabic-imex-mgmt nvcap.
		if err := s.nvdevlib.createNvCapDevice(nvidiaCapFabricImexMgmtPath); err != nil {
			return nil, fmt.Errorf("error creating nvcap device for fabic-imex-mgmt: %w", err)
		}

		// Create new ComputeDomain daemon settings from the ComputeDomainManager.
		computeDomainDaemonSettings := s.computeDomainManager.NewSettings(config.DomainID)

		// Prepare the new ComputeDomain daemon.
		if err := computeDomainDaemonSettings.Prepare(ctx); err != nil {
			return nil, fmt.Errorf("error preparing ComputeDomain daemon settings for requests '%v' in claim '%v': %w", requests, claim.UID, err)
		}

		// Store information about the ComputeDomain daemon in the configState.
		configState.containerEdits = configState.containerEdits.Append(computeDomainDaemonSettings.GetCDIContainerEdits(s.cdi.devRoot, nvcapDeviceInfo))
	}

	return &configState, nil
}

func (s *DeviceState) getConfigResultsMap(rcs *resourceapi.ResourceClaimStatus) (map[runtime.Object][]*resourceapi.DeviceRequestAllocationResult, error) {
	// Retrieve the full set of device configs for the driver.
	configs, err := GetOpaqueDeviceConfigs(
		configapi.Decoder,
		DriverName,
		rcs.Allocation.Devices.Config,
	)
	if err != nil {
		return nil, fmt.Errorf("error getting opaque device configs: %v", err)
	}

	// Add the default ComputeDomainConfig to the front of the config list with the
	// lowest precedence. This guarantees there will be at least one of each
	// config in the list with len(Requests) == 0 for the lookup below.
	configs = slices.Insert(configs, 0, &OpaqueDeviceConfig{
		Requests: []string{},
		Config:   configapi.DefaultComputeDomainChannelConfig(),
	})
	configs = slices.Insert(configs, 0, &OpaqueDeviceConfig{
		Requests: []string{},
		Config:   configapi.DefaultComputeDomainDaemonConfig(),
	})

	// Look through the configs and figure out which one will be applied to
	// each device allocation result based on their order of precedence and type.
	configResultsMap := make(map[runtime.Object][]*resourceapi.DeviceRequestAllocationResult)
	for _, result := range rcs.Allocation.Devices.Results {
		device, exists := s.allocatable[result.Device]
		if !exists {
			return nil, fmt.Errorf("requested device is not allocatable: %v", result.Device)
		}
		for _, c := range slices.Backward(configs) {
			if slices.Contains(c.Requests, result.Request) {
				if _, ok := c.Config.(*configapi.ComputeDomainChannelConfig); ok && device.Type() != ComputeDomainChannelType {
					return nil, fmt.Errorf("cannot apply ComputeDomainChannelConfig to request: %v", result.Request)
				}
				if _, ok := c.Config.(*configapi.ComputeDomainDaemonConfig); ok && device.Type() != ComputeDomainDaemonType {
					return nil, fmt.Errorf("cannot apply ComputeDomainDaemonConfig to request: %v", result.Request)
				}
				configResultsMap[c.Config] = append(configResultsMap[c.Config], &result)
				break
			}
			if len(c.Requests) == 0 {
				if _, ok := c.Config.(*configapi.ComputeDomainChannelConfig); ok && device.Type() != ComputeDomainChannelType {
					continue
				}
				if _, ok := c.Config.(*configapi.ComputeDomainDaemonConfig); ok && device.Type() != ComputeDomainDaemonType {
					continue
				}
				configResultsMap[c.Config] = append(configResultsMap[c.Config], &result)
				break
			}
		}
	}
	return configResultsMap, nil
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
