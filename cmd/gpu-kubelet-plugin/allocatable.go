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
	"slices"

	resourceapi "k8s.io/api/resource/v1"
)

type AllocatableDevices map[string]*AllocatableDevice

func (d AllocatableDevices) GetGPUByPCIeBusID(pcieBusID string) *AllocatableDevice {
	for _, device := range d {
		if device.Type() != GpuDeviceType {
			continue
		}
		if device.Gpu.pcieBusID == pcieBusID {
			return device
		}
	}
	return nil
}

type AllocatableDevice struct {
	Gpu  *GpuInfo
	Mig  *MigDeviceInfo
	Vfio *VfioDeviceInfo
}

func (d AllocatableDevice) Type() string {
	if d.Gpu != nil {
		return GpuDeviceType
	}
	if d.Mig != nil {
		return MigDeviceType
	}
	if d.Vfio != nil {
		return VfioDeviceType
	}
	return UnknownDeviceType
}

func (d *AllocatableDevice) CanonicalName() string {
	switch d.Type() {
	case GpuDeviceType:
		return d.Gpu.CanonicalName()
	case MigDeviceType:
		return d.Mig.CanonicalName()
	case VfioDeviceType:
		return d.Vfio.CanonicalName()
	}
	panic("unexpected type for AllocatableDevice")
}

func (d *AllocatableDevice) GetDevice() resourceapi.Device {
	switch d.Type() {
	case GpuDeviceType:
		return d.Gpu.GetDevice()
	case MigDeviceType:
		return d.Mig.GetDevice()
	case VfioDeviceType:
		return d.Vfio.GetDevice()
	}
	panic("unexpected type for AllocatableDevice")
}

func (d AllocatableDevice) UUID() string {
	if d.Gpu != nil {
		return d.Gpu.UUID
	}
	if d.Mig != nil {
		return d.Mig.UUID
	}
	if d.Vfio != nil {
		return d.Vfio.UUID
	}
	panic("unexpected type for AllocatableDevice")
}

func (d AllocatableDevices) GpuUUIDs() []string {
	var uuids []string
	for _, device := range d {
		if device.Type() == GpuDeviceType {
			uuids = append(uuids, device.Gpu.UUID)
		}
	}
	slices.Sort(uuids)
	return uuids
}

func (d AllocatableDevices) MigDeviceUUIDs() []string {
	var uuids []string
	for _, device := range d {
		if device.Type() == MigDeviceType {
			uuids = append(uuids, device.Mig.UUID)
		}
	}
	slices.Sort(uuids)
	return uuids
}

func (d AllocatableDevices) VfioDeviceUUIDs() []string {
	var uuids []string
	for _, device := range d {
		if device.Type() == VfioDeviceType {
			uuids = append(uuids, device.Vfio.UUID)
		}
	}
	slices.Sort(uuids)
	return uuids
}

func (d AllocatableDevices) UUIDs() []string {
	uuids := append(d.GpuUUIDs(), d.MigDeviceUUIDs()...)
	uuids = append(uuids, d.VfioDeviceUUIDs()...)
	slices.Sort(uuids)
	return uuids
}
