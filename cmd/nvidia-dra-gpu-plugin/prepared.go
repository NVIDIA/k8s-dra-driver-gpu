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
	"slices"

	drapbv1 "k8s.io/kubelet/pkg/apis/dra/v1beta1"
)

type PreparedDeviceList []PreparedDevice
type PreparedDevices []*PreparedDeviceGroup
type PreparedClaims map[string]PreparedDevices

type PreparedDevice struct {
	Gpu *PreparedGpu       `json:"gpu"`
	Mig *PreparedMigDevice `json:"mig"`
}

type PreparedGpu struct {
	Info   *GpuInfo        `json:"info"`
	Device *drapbv1.Device `json:"device"`
}

type PreparedMigDevice struct {
	Info   *MigDeviceInfo  `json:"info"`
	Device *drapbv1.Device `json:"device"`
}

type PreparedDeviceGroup struct {
	Devices     PreparedDeviceList `json:"devices"`
	ConfigState DeviceConfigState  `json:"configState"`
}

func (d PreparedDevice) Type() string {
	if d.Gpu != nil {
		return GpuDeviceType
	}
	if d.Mig != nil {
		return MigDeviceType
	}
	return UnknownDeviceType
}

func (d *PreparedDevice) CanonicalName() string {
	switch d.Type() {
	case GpuDeviceType:
		return d.Gpu.Info.CanonicalName()
	case MigDeviceType:
		return d.Mig.Info.CanonicalName()
	}
	panic("unexpected type for AllocatableDevice")
}

func (d *PreparedDevice) CanonicalIndex() string {
	switch d.Type() {
	case GpuDeviceType:
		return d.Gpu.Info.CanonicalIndex()
	case MigDeviceType:
		return d.Mig.Info.CanonicalIndex()
	}
	panic("unexpected type for AllocatableDevice")
}

func (l PreparedDeviceList) Gpus() PreparedDeviceList {
	var devices PreparedDeviceList
	for _, device := range l {
		if device.Type() == GpuDeviceType {
			devices = append(devices, device)
		}
	}
	return devices
}

func (l PreparedDeviceList) MigDevices() PreparedDeviceList {
	var devices PreparedDeviceList
	for _, device := range l {
		if device.Type() == MigDeviceType {
			devices = append(devices, device)
		}
	}
	return devices
}

func (d PreparedDevices) GetDevices() []*drapbv1.Device {
	var devices []*drapbv1.Device
	for _, group := range d {
		devices = append(devices, group.GetDevices()...)
	}
	return devices
}

func (g *PreparedDeviceGroup) GetDevices() []*drapbv1.Device {
	var devices []*drapbv1.Device
	for _, device := range g.Devices {
		switch device.Type() {
		case GpuDeviceType:
			devices = append(devices, device.Gpu.Device)
		case MigDeviceType:
			devices = append(devices, device.Mig.Device)
		}
	}
	return devices
}

func (l PreparedDeviceList) UUIDs() []string {
	uuids := append(l.GpuUUIDs(), l.MigDeviceUUIDs()...)
	slices.Sort(uuids)
	return uuids
}

func (g *PreparedDeviceGroup) UUIDs() []string {
	uuids := append(g.GpuUUIDs(), g.MigDeviceUUIDs()...)
	slices.Sort(uuids)
	return uuids
}

func (d PreparedDevices) UUIDs() []string {
	uuids := append(d.GpuUUIDs(), d.MigDeviceUUIDs()...)
	slices.Sort(uuids)
	return uuids
}

func (l PreparedDeviceList) GpuUUIDs() []string {
	var uuids []string
	for _, device := range l.Gpus() {
		uuids = append(uuids, device.Gpu.Info.UUID)
	}
	slices.Sort(uuids)
	return uuids
}

func (g *PreparedDeviceGroup) GpuUUIDs() []string {
	return g.Devices.Gpus().UUIDs()
}

func (d PreparedDevices) GpuUUIDs() []string {
	var uuids []string
	for _, group := range d {
		uuids = append(uuids, group.GpuUUIDs()...)
	}
	slices.Sort(uuids)
	return uuids
}

func (l PreparedDeviceList) MigDeviceUUIDs() []string {
	var uuids []string
	for _, device := range l.MigDevices() {
		uuids = append(uuids, device.Mig.Info.UUID)
	}
	slices.Sort(uuids)
	return uuids
}

func (g *PreparedDeviceGroup) MigDeviceUUIDs() []string {
	return g.Devices.MigDevices().UUIDs()
}

func (d PreparedDevices) MigDeviceUUIDs() []string {
	var uuids []string
	for _, group := range d {
		uuids = append(uuids, group.MigDeviceUUIDs()...)
	}
	slices.Sort(uuids)
	return uuids
}
