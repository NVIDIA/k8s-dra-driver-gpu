/*
 * Copyright (c) 2022-2023, NVIDIA CORPORATION.  All rights reserved.
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
	"fmt"
	"io"

	"github.com/sirupsen/logrus"

	nvdevice "github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/nvidia-container-toolkit/pkg/nvcdi"
	"github.com/NVIDIA/nvidia-container-toolkit/pkg/nvcdi/spec"
	transformroot "github.com/NVIDIA/nvidia-container-toolkit/pkg/nvcdi/transform/root"
	"k8s.io/klog/v2"
	cdiapi "tags.cncf.io/container-device-interface/pkg/cdi"
	cdiparser "tags.cncf.io/container-device-interface/pkg/parser"
	cdispec "tags.cncf.io/container-device-interface/specs-go"

	"github.com/NVIDIA/k8s-dra-driver-gpu/internal/common"
	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
)

const (
	cdiVendor = "k8s." + DriverName

	cdiDeviceClass = "device"
	cdiDeviceKind  = cdiVendor + "/" + cdiDeviceClass
	cdiClaimClass  = "claim"
	cdiClaimKind   = cdiVendor + "/" + cdiClaimClass

	cdiBaseSpecIdentifier = "base"
	cdiVfioSpecIdentifier = "vfio"

	defaultCDIRoot = "/var/run/cdi"
	procNvCapsPath = "/proc/driver/nvidia/capabilities"
)

type CDIHandler struct {
	logger            *logrus.Logger
	nvml              nvml.Interface
	nvdevice          nvdevice.Interface
	nvcdiDevice       nvcdi.Interface
	nvcdiClaim        nvcdi.Interface
	cache             *cdiapi.Cache
	driverRoot        string
	devRoot           string
	targetDriverRoot  string
	nvidiaCDIHookPath string

	cdiRoot     string
	vendor      string
	deviceClass string
	claimClass  string
}

func NewCDIHandler(opts ...cdiOption) (*CDIHandler, error) {
	h := &CDIHandler{}
	for _, opt := range opts {
		opt(h)
	}

	if h.logger == nil {
		h.logger = logrus.New()
		h.logger.SetOutput(io.Discard)
	}
	if h.nvml == nil {
		h.nvml = nvml.New()
	}
	if h.cdiRoot == "" {
		h.cdiRoot = defaultCDIRoot
	}
	if h.nvdevice == nil {
		h.nvdevice = nvdevice.New(h.nvml)
	}
	if h.vendor == "" {
		h.vendor = cdiVendor
	}
	if h.deviceClass == "" {
		h.deviceClass = cdiDeviceClass
	}
	if h.claimClass == "" {
		h.claimClass = cdiClaimClass
	}
	if h.nvcdiDevice == nil {
		nvcdilib, err := nvcdi.New(
			nvcdi.WithDeviceLib(h.nvdevice),
			nvcdi.WithDriverRoot(h.driverRoot),
			nvcdi.WithDevRoot(h.devRoot),
			nvcdi.WithLogger(h.logger),
			nvcdi.WithNvmlLib(h.nvml),
			nvcdi.WithMode("nvml"),
			nvcdi.WithVendor(h.vendor),
			nvcdi.WithClass(h.deviceClass),
			nvcdi.WithNVIDIACDIHookPath(h.nvidiaCDIHookPath),
		)
		if err != nil {
			return nil, fmt.Errorf("unable to create CDI library for devices: %w", err)
		}
		h.nvcdiDevice = nvcdilib
	}
	if h.nvcdiClaim == nil {
		nvcdilib, err := nvcdi.New(
			nvcdi.WithDeviceLib(h.nvdevice),
			nvcdi.WithDriverRoot(h.driverRoot),
			nvcdi.WithDevRoot(h.devRoot),
			nvcdi.WithLogger(h.logger),
			nvcdi.WithNvmlLib(h.nvml),
			nvcdi.WithMode("nvml"),
			nvcdi.WithVendor(h.vendor),
			nvcdi.WithClass(h.claimClass),
			nvcdi.WithNVIDIACDIHookPath(h.nvidiaCDIHookPath),
		)
		if err != nil {
			return nil, fmt.Errorf("unable to create CDI library for claims: %w", err)
		}
		h.nvcdiClaim = nvcdilib
	}
	if h.cache == nil {
		cache, err := cdiapi.NewCache(
			cdiapi.WithSpecDirs(h.cdiRoot),
		)
		if err != nil {
			return nil, fmt.Errorf("unable to create a new CDI cache: %w", err)
		}
		h.cache = cache
	}

	return h, nil
}

func (cdi *CDIHandler) writeSpec(spec spec.Interface, specName string) error {
	// Transform the spec to make it aware that it is running inside a container.
	err := transformroot.New(
		transformroot.WithRoot(cdi.driverRoot),
		transformroot.WithTargetRoot(cdi.targetDriverRoot),
		transformroot.WithRelativeTo("host"),
	).Transform(spec.Raw())
	if err != nil {
		return fmt.Errorf("failed to transform driver root in CDI spec: %w", err)
	}

	// Update the spec to include only the minimum version necessary.
	minVersion, err := cdispec.MinimumRequiredVersion(spec.Raw())
	if err != nil {
		return fmt.Errorf("failed to get minimum required CDI spec version: %w", err)
	}
	spec.Raw().Version = minVersion

	// Write the spec out to disk.
	return cdi.cache.WriteSpec(spec.Raw(), specName)

}

func (cdi *CDIHandler) CreateStandardDeviceSpecFile(allocatable AllocatableDevices) error {
	if err := cdi.createStandardNvidiaDeviceSpecFile(allocatable); err != nil {
		klog.Errorf("failed to create standard nvidia device spec file: %v", err)
		return err
	}

	if featuregates.Enabled(featuregates.PassthroughSupport) {
		if err := cdi.createStandardVfioDeviceSpecFile(allocatable); err != nil {
			klog.Errorf("failed to create standard vfio device spec file: %v", err)
			return err
		}
	}
	return nil
}

func (cdi *CDIHandler) createStandardVfioDeviceSpecFile(allocatable AllocatableDevices) error {
	commonEdits := GetVfioCommonCDIContainerEdits()
	var deviceSpecs []cdispec.Device
	for _, device := range allocatable {
		if device.Type() != VfioDeviceType {
			continue
		}
		edits := GetVfioCDIContainerEdits(device.Vfio)
		dspec := cdispec.Device{
			Name:           device.CanonicalName(),
			ContainerEdits: *edits.ContainerEdits,
		}
		deviceSpecs = append(deviceSpecs, dspec)
	}

	if len(deviceSpecs) == 0 {
		return nil
	}

	spec, err := spec.New(
		spec.WithVendor(cdiVendor),
		spec.WithClass(cdiDeviceClass),
		spec.WithDeviceSpecs(deviceSpecs),
		spec.WithEdits(*commonEdits.ContainerEdits),
	)
	if err != nil {
		return fmt.Errorf("failed to creat CDI spec: %w", err)
	}

	specName := cdiapi.GenerateTransientSpecName(cdiVendor, cdiDeviceClass, cdiVfioSpecIdentifier)
	klog.Infof("Writing vfio spec for %s to %s", specName, cdi.cdiRoot)
	return cdi.writeSpec(spec, specName)
}

func (cdi *CDIHandler) createStandardNvidiaDeviceSpecFile(allocatable AllocatableDevices) error {
	// Initialize NVML in order to get the device edits.
	if r := cdi.nvml.Init(); r != nvml.SUCCESS {
		return fmt.Errorf("failed to initialize NVML: %v", r)
	}
	defer func() {
		if r := cdi.nvml.Shutdown(); r != nvml.SUCCESS {
			klog.Warningf("failed to shutdown NVML: %v", r)
		}
	}()

	// Generate the set of common edits.
	commonEdits, err := cdi.nvcdiDevice.GetCommonEdits()
	if err != nil {
		return fmt.Errorf("failed to get common CDI spec edits: %w", err)
	}

	// Make sure that NVIDIA_VISIBLE_DEVICES is set to void to avoid the
	// nvidia-container-runtime honoring it in addition to the underlying
	// runtime honoring CDI.
	commonEdits.Env = append(
		commonEdits.Env,
		"NVIDIA_VISIBLE_DEVICES=void")

	// Generate device specs for all full GPUs and MIG devices.
	var deviceSpecs []cdispec.Device
	for _, device := range allocatable {
		if device.Type() == VfioDeviceType {
			continue
		}

		uuid := device.UUID()
		if device.Type() == MigDeviceType {
			// Goal: inject parent dev node. Other dev nodes specific to this
			// MIG device are injected 'manually' further below. That is because
			// currently `nvcdiDevice.GetDeviceSpecsByID()` may yield an
			// incomplete spec for MIG devices, see
			// https://github.com/NVIDIA/k8s-dra-driver-gpu/issues/787. Instead,
			// manually create the required dev node spec for the consuming
			// container via GetDevNodesForMigDevice() below.
			uuid = device.Mig.parent.UUID
		}

		dspecs, err := cdi.nvcdiDevice.GetDeviceSpecsByID(uuid)
		if err != nil {
			return fmt.Errorf("unable to get device spec for %s: %w", device.CanonicalName(), err)
		}
		dspecs[0].Name = device.CanonicalName()

		if device.Type() == MigDeviceType {
			devnodesForMig, err := cdi.GetDevNodesForMigDevice(device.Mig.parent.minor, int(device.Mig.giInfo.Id), int(device.Mig.ciInfo.Id))
			if err != nil {
				return fmt.Errorf("failed to construct MIG device DeviceNode edits: %w", err)
			}
			klog.V(7).Infof("CDI spec: appending MIG device nodes")
			dspecs[0].ContainerEdits.DeviceNodes = append(dspecs[0].ContainerEdits.DeviceNodes, devnodesForMig...)
		}

		deviceSpecs = append(deviceSpecs, dspecs[0])
	}

	// Generate base spec from commonEdits and deviceEdits.
	spec, err := spec.New(
		spec.WithVendor(cdiVendor),
		spec.WithClass(cdiDeviceClass),
		spec.WithDeviceSpecs(deviceSpecs),
		spec.WithEdits(*commonEdits.ContainerEdits),
	)
	if err != nil {
		return fmt.Errorf("failed to creat CDI spec: %w", err)
	}

	specName := cdiapi.GenerateTransientSpecName(cdiVendor, cdiDeviceClass, cdiBaseSpecIdentifier)
	klog.Infof("Writing spec for %s to %s", specName, cdi.cdiRoot)
	return cdi.writeSpec(spec, specName)
}

func (cdi *CDIHandler) CreateClaimSpecFile(claimUID string, preparedDevices PreparedDevices) error {
	// Generate claim specific specs for each device.
	var deviceSpecs []cdispec.Device
	for _, group := range preparedDevices {
		// If there are no edits passed back as part of the device config state, skip it
		if group.ConfigState.containerEdits == nil {
			continue
		}

		// Apply any edits passed back as part of the device config state to all devices
		for _, device := range group.Devices {
			deviceSpec := cdispec.Device{
				Name:           fmt.Sprintf("%s-%s", claimUID, device.CanonicalName()),
				ContainerEdits: *group.ConfigState.containerEdits.ContainerEdits,
			}

			deviceSpecs = append(deviceSpecs, deviceSpec)
		}
	}

	// If there are no claim specific deviceSpecs, just return without creating the spec file
	if len(deviceSpecs) == 0 {
		return nil
	}

	// Generate the claim specific device spec for this driver.
	spec, err := spec.New(
		spec.WithVendor(cdiVendor),
		spec.WithClass(cdiClaimClass),
		spec.WithDeviceSpecs(deviceSpecs),
	)
	if err != nil {
		return fmt.Errorf("failed to creat CDI spec: %w", err)
	}

	// Write the spec out to disk.
	specName := cdiapi.GenerateTransientSpecName(cdiVendor, cdiClaimClass, claimUID)
	klog.Infof("Writing claim spec for %s to %s", specName, cdi.cdiRoot)
	return cdi.writeSpec(spec, specName)
}

func (cdi *CDIHandler) DeleteClaimSpecFile(claimUID string) error {
	specName := cdiapi.GenerateTransientSpecName(cdiVendor, cdiClaimClass, claimUID)
	return cdi.cache.RemoveSpec(specName)
}

func (cdi *CDIHandler) GetStandardDevice(device *AllocatableDevice) string {
	return cdiparser.QualifiedName(cdiVendor, cdiDeviceClass, device.CanonicalName())
}

func (cdi *CDIHandler) GetClaimDevice(claimUID string, device *AllocatableDevice, containerEdits *cdiapi.ContainerEdits) string {
	if containerEdits == nil {
		return ""
	}
	return cdiparser.QualifiedName(cdiVendor, cdiClaimClass, fmt.Sprintf("%s-%s", claimUID, device.CanonicalName()))
}

// Construct and return the CDI `deviceNodes` specification for the two
// character devices `/dev/nvidia-caps/nvidia-cap<CIm>` and
// `/dev/nvidia-caps/nvidia-cap<GIm>` for a specific MIG device.
//
// Context: for containerized workload to see and use a specific MIG device, it
// needs to be able to open three character device nodes:
//
// 1) `/dev/nvidia<Pm>`, with <Pm> referring to the parent's minor. This exists
// on the host.
//
// 2) /dev/nvidia-caps/nvidia-cap<CIm> and /dev/nvidia-caps/nvidia-cap<GIm>,
// with <GIm> and <CIm> referring to the MIG GPU instance's and Compute
// instance's minor, respectively. For the the latter two device nodes it is
// sufficient to create them in the container (with proper cgroups permissions),
// without actually requiring the same device nodes to be explicitly created on
// the host. That is what is achieved below with the structure created in
// cdiDevNodeFromNVCapDevInfo().
func (cdi *CDIHandler) GetDevNodesForMigDevice(parentMinor int, giId int, ciId int) ([]*cdispec.DeviceNode, error) {
	gipath := fmt.Sprintf("%s/gpu%d/mig/gi%d/access", procNvCapsPath, parentMinor, giId)
	cipath := fmt.Sprintf("%s/gpu%d/mig/gi%d/ci%d/access", procNvCapsPath, parentMinor, giId, ciId)

	giCapsInfo, err := common.ParseNVCapDeviceInfo(gipath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse GI capabilities file %s: %w", gipath, err)
	}

	ciCapsInfo, err := common.ParseNVCapDeviceInfo(cipath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CI capabilities file %s: %w", cipath, err)
	}

	devnodes := []*cdispec.DeviceNode{giCapsInfo.CDICharDevNode(), ciCapsInfo.CDICharDevNode()}
	return devnodes, nil
}
