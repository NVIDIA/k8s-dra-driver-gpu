/*
 * Copyright (c) 2024, NVIDIA CORPORATION.  All rights reserved.
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

	"github.com/Masterminds/semver/v3"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/dynamic-resource-allocation/deviceattribute"
	"k8s.io/utils/ptr"
)

// Defined similarly as https://pkg.go.dev/k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1#Healthy.
type HealthStatus string

const (
	Healthy HealthStatus = "Healthy"
	// With NVMLDeviceHealthCheck, Unhealthy means that there are critcal xid errors on the device.
	Unhealthy HealthStatus = "Unhealthy"
)

// Represents a specific, full, physical GPU device.
type GpuInfo struct {
	UUID                  string `json:"uuid"`
	minor                 int
	migEnabled            bool
	vfioEnabled           bool
	memoryBytes           uint64
	productName           string
	brand                 string
	architecture          string
	cudaComputeCapability string
	driverVersion         string
	cudaDriverVersion     string
	pcieBusID             string
	pcieRootAttr          *deviceattribute.DeviceAttribute
	migProfiles           []*MigProfileInfo
	addressingMode        *string
	health                HealthStatus

	// The following properties that can only be known after inspecting MIG
	// profiles.
	maxCapacities PartCapacityMap
	memSliceCount int
}

// Represents a specific (concrete, incarnated, created) MIG device. Annotated
// properties are stored in the checkpoint JSON upon prepare.
type MigDeviceInfo struct {
	// Selectively serialize some properties to the checkpoint JSON file (needed
	// mainly for controlled deletion in the unprepare flow).

	UUID        string `json:"uuid"`
	Profile     string `json:"profile"`
	ParentUUID  string `json:"parentUUID"`
	GiProfileID int    `json:"profileId"`

	// TODO: maybe embed MigLiveTuple.
	ParentMinor int `json:"parentMinor"`
	CIID        int `json:"ciId"`
	GIID        int `json:"giId"`

	// Store PlacementStart in the JSON checkpoint because in CanonicalName() we
	// rely on this -- and this must work after JSON deserialization.
	PlacementStart int `json:"placementStart"`
	PlacementSize  int `json:"placementSize"`

	gIInfo        *nvml.GpuInstanceInfo
	cIInfo        *nvml.ComputeInstanceInfo
	parent        *GpuInfo
	giProfileInfo *nvml.GpuInstanceProfileInfo
	ciProfileInfo *nvml.ComputeInstanceProfileInfo
	pcieBusID     string
	pcieRootAttr  *deviceattribute.DeviceAttribute
	health        HealthStatus
}

type VfioDeviceInfo struct {
	UUID                   string `json:"uuid"`
	deviceID               string
	vendorID               string
	index                  int
	parent                 *GpuInfo
	productName            string
	pcieBusID              string
	pcieRootAttr           *deviceattribute.DeviceAttribute
	numaNode               int
	iommuGroup             int
	addressableMemoryBytes uint64
}

// CanonicalName returns the nameused for device announcement (in ResourceSlice
// objects). There is quite a bit of history to using the minor number for
// device announcement. Some context can be found at
// https://github.com/NVIDIA/k8s-dra-driver-gpu/issues/563#issuecomment-3345631087.
func (d *GpuInfo) CanonicalName() DeviceName {
	return fmt.Sprintf("gpu-%d", d.minor)
}

// String returns both the GPU minor for easy recognizability, but also the
// UUID for precision. It is intended for usage in log messages.
func (d *GpuInfo) String() string {
	return fmt.Sprintf("%s-%s", d.CanonicalName(), d.UUID)
}

func (m *MigDeviceInfo) SpecTuple() *MigSpecTuple {
	return &MigSpecTuple{
		ParentMinor:    m.ParentMinor,
		ProfileID:      m.GiProfileID,
		PlacementStart: m.PlacementStart,
	}
}

func (m *MigDeviceInfo) LiveTuple() *MigLiveTuple {
	return &MigLiveTuple{
		ParentMinor: m.ParentMinor,
		ParentUUID:  m.ParentUUID,
		GIID:        m.GIID,
		CIID:        m.CIID,
		MigUUID:     m.UUID,
	}
}

// Return the canonical MIG device name. The name unambiguously defines the
// physical configuration, but doesn't reflect the fact that this represents a
// curently-live MIG device.
func (d *MigDeviceInfo) CanonicalName() string {
	return d.SpecTuple().ToCanonicalName(d.Profile)
}

func (d *VfioDeviceInfo) CanonicalName() string {
	return fmt.Sprintf("gpu-vfio-%d", d.index)
}

// Populate internal data structures -- detail that is only known after
// inspecting all individual MIG profiles associated with this physical GPU.
func (d *GpuInfo) AddDetailAfterWalkingMigProfiles(maxcap PartCapacityMap, memSliceCount int) {
	d.maxCapacities = maxcap
	d.memSliceCount = memSliceCount
}

func (d *GpuInfo) GetDevice() resourceapi.Device {
	// TODO: Consume GetPCIBusIDAttribute from https://github.com/kubernetes/kubernetes/blob/4c5746c0bc529439f78af458f8131b5def4dbe5d/staging/src/k8s.io/dynamic-resource-allocation/deviceattribute/attribute.go#L39
	device := resourceapi.Device{
		Name:       d.CanonicalName(),
		Attributes: d.PartDevAttributes(),
		Capacity: map[resourceapi.QualifiedName]resourceapi.DeviceCapacity{
			"memory": {
				Value: *resource.NewQuantity(int64(d.memoryBytes), resource.BinarySI),
			},
		},
	}
	if d.pcieRootAttr != nil {
		device.Attributes[d.pcieRootAttr.Name] = d.pcieRootAttr.Value
	}
	if d.addressingMode != nil {
		device.Attributes["addressingMode"] = resourceapi.DeviceAttribute{
			StringValue: d.addressingMode,
		}
	}
	return device
}

func (d *MigDeviceInfo) GetDevice() resourceapi.Device {
	// TODO: Consume GetPCIBusIDAttribute from https://github.com/kubernetes/kubernetes/blob/4c5746c0bc529439f78af458f8131b5def4dbe5d/staging/src/k8s.io/dynamic-resource-allocation/deviceattribute/attribute.go#L39
	pciBusIDAttrName := resourceapi.QualifiedName(deviceattribute.StandardDeviceAttributePrefix + "pciBusID")
	device := resourceapi.Device{
		Name: d.CanonicalName(),
		Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"type": {
				// Note: for API stability, it's critical we use the string
				// "mig" here. In order to not confuse this with implementation
				// details, I've now hard-coded it here. Replace again with a
				// constant when we make sure that this constant is used wisely
				// across the codebase. This was const `MigDeviceType` before
				// introduction of the dyn MIG feature.
				StringValue: ptr.To("mig"),
			},
			"uuid": {
				StringValue: &d.UUID,
			},
			"parentUUID": {
				StringValue: &d.parent.UUID,
			},
			"profile": {
				StringValue: &d.Profile,
			},
			"productName": {
				StringValue: &d.parent.productName,
			},
			"brand": {
				StringValue: &d.parent.brand,
			},
			"architecture": {
				StringValue: &d.parent.architecture,
			},
			"cudaComputeCapability": {
				VersionValue: ptr.To(semver.MustParse(d.parent.cudaComputeCapability).String()),
			},
			"driverVersion": {
				VersionValue: ptr.To(semver.MustParse(d.parent.driverVersion).String()),
			},
			"cudaDriverVersion": {
				VersionValue: ptr.To(semver.MustParse(d.parent.cudaDriverVersion).String()),
			},
			pciBusIDAttrName: {
				StringValue: &d.pcieBusID,
			},
		},
		Capacity: map[resourceapi.QualifiedName]resourceapi.DeviceCapacity{
			"multiprocessors": {
				Value: *resource.NewQuantity(int64(d.giProfileInfo.MultiprocessorCount), resource.BinarySI),
			},
			"copyEngines": {Value: *resource.NewQuantity(int64(d.giProfileInfo.CopyEngineCount), resource.BinarySI)},
			"decoders":    {Value: *resource.NewQuantity(int64(d.giProfileInfo.DecoderCount), resource.BinarySI)},
			"encoders":    {Value: *resource.NewQuantity(int64(d.giProfileInfo.EncoderCount), resource.BinarySI)},
			"jpegEngines": {Value: *resource.NewQuantity(int64(d.giProfileInfo.JpegCount), resource.BinarySI)},
			"ofaEngines":  {Value: *resource.NewQuantity(int64(d.giProfileInfo.OfaCount), resource.BinarySI)},
			// `memoryBytes` would be more expressive -- but in the k8s
			// landscape, that ship has long sailed: container limits for
			// example also use `memory`. Note that giProfileInfo.MemorySizeMB
			// has a misleading name -- think of it as MemorySizeMiB (that's
			// documented in the NVML source).
			"memory": {Value: *resource.NewQuantity(int64(d.giProfileInfo.MemorySizeMB*1024*1024), resource.BinarySI)},
		},
	}

	// Note(JP): noted elsewhere; what's the purpose of announcing memory slices
	// as capacity? Are users interested? That effectively shows 'placement' to
	// users. Does it also allow users to request placement? Do we want to allow
	// users to request specific placement?
	for i := d.PlacementStart; i < d.PlacementStart+d.PlacementSize; i++ {
		// TODO: review memorySlice (legacy) vs memory-slice -- I believe I
		// prefer memory-slice because that works for counters. Do we even need
		// to announce the slices as capacity?
		capacity := resourceapi.QualifiedName(fmt.Sprintf("memorySlice%d", i))
		device.Capacity[capacity] = resourceapi.DeviceCapacity{
			Value: *resource.NewQuantity(1, resource.BinarySI),
		}
	}
	if d.pcieRootAttr != nil {
		device.Attributes[d.pcieRootAttr.Name] = d.pcieRootAttr.Value
	}
	if d.parent.addressingMode != nil {
		device.Attributes["addressingMode"] = resourceapi.DeviceAttribute{
			StringValue: d.parent.addressingMode,
		}
	}
	return device
}

func (d *VfioDeviceInfo) GetDevice() resourceapi.Device {
	// TODO: Consume GetPCIBusIDAttribute from https://github.com/kubernetes/kubernetes/blob/4c5746c0bc529439f78af458f8131b5def4dbe5d/staging/src/k8s.io/dynamic-resource-allocation/deviceattribute/attribute.go#L39
	pciBusIDAttrName := resourceapi.QualifiedName(deviceattribute.StandardDeviceAttributePrefix + "pciBusID")
	device := resourceapi.Device{
		Name: d.CanonicalName(),
		Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
			"type": {
				StringValue: ptr.To(VfioDeviceType),
			},
			"uuid": {
				StringValue: &d.UUID,
			},
			"deviceID": {
				StringValue: &d.deviceID,
			},
			"vendorID": {
				StringValue: &d.vendorID,
			},
			"numa": {
				IntValue: ptr.To(int64(d.numaNode)),
			},
			pciBusIDAttrName: {
				StringValue: &d.pcieBusID,
			},
			"productName": {
				StringValue: &d.productName,
			},
		},
		Capacity: map[resourceapi.QualifiedName]resourceapi.DeviceCapacity{
			"addressableMemory": {
				Value: *resource.NewQuantity(int64(d.addressableMemoryBytes), resource.BinarySI),
			},
		},
	}
	if d.pcieRootAttr != nil {
		device.Attributes[d.pcieRootAttr.Name] = d.pcieRootAttr.Value
	}
	return device
}
