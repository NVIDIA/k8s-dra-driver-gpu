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

	"github.com/Masterminds/semver"
	nvdev "github.com/NVIDIA/go-nvlib/pkg/nvlib/device"
	"github.com/NVIDIA/go-nvml/pkg/nvml"
	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

const (
	PcieRootDeviceAttribute = "resource.kubernetes.io/pcieRoot"
)

type GpuInfo struct {
	UUID                  string `json:"uuid"`
	index                 int
	minor                 int
	migEnabled            bool
	memoryBytes           uint64
	productName           string
	brand                 string
	architecture          string
	pcieRoot              string
	cudaComputeCapability string
	driverVersion         string
	cudaDriverVersion     string
	migProfiles           []*MigProfileInfo
}

type MigDeviceInfo struct {
	UUID          string `json:"uuid"`
	index         int
	profile       string
	parent        *GpuInfo
	placement     *MigDevicePlacement
	giProfileInfo *nvml.GpuInstanceProfileInfo
	giInfo        *nvml.GpuInstanceInfo
	ciProfileInfo *nvml.ComputeInstanceProfileInfo
	ciInfo        *nvml.ComputeInstanceInfo
}

type MigProfileInfo struct {
	profile    nvdev.MigProfile
	placements []*MigDevicePlacement
}

type MigDevicePlacement struct {
	nvml.GpuInstancePlacement
}

func (p MigProfileInfo) String() string {
	return p.profile.String()
}

func (d *GpuInfo) CanonicalName() string {
	return fmt.Sprintf("gpu-%d", d.index)
}

func (d *MigDeviceInfo) CanonicalName() string {
	return fmt.Sprintf("gpu-%d-mig-%d-%d-%d", d.parent.index, d.giInfo.ProfileId, d.placement.Start, d.placement.Size)
}

func (d *GpuInfo) CanonicalIndex() string {
	return fmt.Sprintf("%d", d.index)
}

func (d *MigDeviceInfo) CanonicalIndex() string {
	return fmt.Sprintf("%d:%d", d.parent.index, d.index)
}

func (d *GpuInfo) GetDevice() resourceapi.Device {
	device := resourceapi.Device{
		Name: d.CanonicalName(),
		Basic: &resourceapi.BasicDevice{
			Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				"type": {
					StringValue: ptr.To(GpuDeviceType),
				},
				"uuid": {
					StringValue: &d.UUID,
				},
				"minor": {
					IntValue: ptr.To(int64(d.minor)),
				},
				"index": {
					IntValue: ptr.To(int64(d.index)),
				},
				"productName": {
					StringValue: &d.productName,
				},
				"brand": {
					StringValue: &d.brand,
				},
				"architecture": {
					StringValue: &d.architecture,
				},
				"cudaComputeCapability": {
					VersionValue: ptr.To(semver.MustParse(d.cudaComputeCapability).String()),
				},
				"driverVersion": {
					VersionValue: ptr.To(semver.MustParse(d.driverVersion).String()),
				},
				"cudaDriverVersion": {
					VersionValue: ptr.To(semver.MustParse(d.cudaDriverVersion).String()),
				},

				// Kubernetes standardized attributes
				PcieRootDeviceAttribute: {
					StringValue: &d.pcieRoot,
				},
			},
			Capacity: map[resourceapi.QualifiedName]resourceapi.DeviceCapacity{
				"memory": {
					Value: *resource.NewQuantity(int64(d.memoryBytes), resource.BinarySI),
				},
			},
		},
	}
	return device
}

func (d *MigDeviceInfo) GetDevice() resourceapi.Device {
	device := resourceapi.Device{
		Name: d.CanonicalName(),
		Basic: &resourceapi.BasicDevice{
			Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				"type": {
					StringValue: ptr.To(MigDeviceType),
				},
				"uuid": {
					StringValue: &d.UUID,
				},
				"parentUUID": {
					StringValue: &d.parent.UUID,
				},
				"index": {
					IntValue: ptr.To(int64(d.index)),
				},
				"parentIndex": {
					IntValue: ptr.To(int64(d.parent.index)),
				},
				"profile": {
					StringValue: &d.profile,
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

				// Kubernetes standardized attributes
				PcieRootDeviceAttribute: {
					StringValue: &d.parent.pcieRoot,
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
				"memory":      {Value: *resource.NewQuantity(int64(d.giProfileInfo.MemorySizeMB*1024*1024), resource.BinarySI)},
			},
		},
	}
	for i := d.placement.Start; i < d.placement.Start+d.placement.Size; i++ {
		capacity := resourceapi.QualifiedName(fmt.Sprintf("memorySlice%d", i))
		device.Basic.Capacity[capacity] = resourceapi.DeviceCapacity{
			Value: *resource.NewQuantity(1, resource.BinarySI),
		}
	}
	return device
}
