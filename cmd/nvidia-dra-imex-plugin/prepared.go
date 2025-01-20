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
	drapbv1 "k8s.io/kubelet/pkg/apis/dra/v1beta1"
)

type PreparedDeviceList []PreparedDevice
type PreparedDevices []*PreparedDeviceGroup
type PreparedClaims map[string]PreparedDevices

type PreparedDevice struct {
	ImexChannel *PreparedImexChannel `json:"imexChannel"`
}

type PreparedImexChannel struct {
	Info   *ImexChannelInfo `json:"info"`
	Device *drapbv1.Device  `json:"device"`
}

type PreparedDeviceGroup struct {
	Devices     PreparedDeviceList `json:"devices"`
	ConfigState DeviceConfigState  `json:"configState"`
}

func (d PreparedDevice) Type() string {
	if d.ImexChannel != nil {
		return ImexChannelType
	}
	return UnknownDeviceType
}

func (d *PreparedDevice) CanonicalName() string {
	switch d.Type() {
	case ImexChannelType:
		return d.ImexChannel.Info.CanonicalName()
	}
	panic("unexpected type for AllocatableDevice")
}

func (d *PreparedDevice) CanonicalIndex() string {
	switch d.Type() {
	case ImexChannelType:
		return d.ImexChannel.Info.CanonicalIndex()
	}
	panic("unexpected type for AllocatableDevice")
}

func (l PreparedDeviceList) ImexChannels() PreparedDeviceList {
	var devices PreparedDeviceList
	for _, device := range l {
		if device.Type() == ImexChannelType {
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
		case ImexChannelType:
			devices = append(devices, device.ImexChannel.Device)
		}
	}
	return devices
}
