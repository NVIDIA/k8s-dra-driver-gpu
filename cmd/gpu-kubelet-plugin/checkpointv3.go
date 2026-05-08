/*
Copyright The Kubernetes Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/dynamic-resource-allocation/kubeletplugin"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/checksum"
)

type CheckpointDevice struct {
	Requests     []string `json:"requests,omitempty"`
	PoolName     string   `json:"poolName,omitempty"`
	DeviceName   string   `json:"deviceName,omitempty"`
	CDIDeviceIDs []string `json:"cdiDeviceIDs,omitempty"`
}

func NewCheckpointDevice(d *kubeletplugin.Device) *CheckpointDevice {
	if d == nil {
		return nil
	}
	return &CheckpointDevice{
		Requests:     d.Requests,
		PoolName:     d.PoolName,
		DeviceName:   d.DeviceName,
		CDIDeviceIDs: d.CDIDeviceIDs,
	}
}

func (c *CheckpointDevice) ToKubeletPluginDevice() *kubeletplugin.Device {
	if c == nil {
		return nil
	}
	return &kubeletplugin.Device{
		Requests:     c.Requests,
		PoolName:     c.PoolName,
		DeviceName:   c.DeviceName,
		CDIDeviceIDs: c.CDIDeviceIDs,
	}
}

// V3 device tree. The shape mirrors the in-memory PreparedDevices tree from
// prepared.go, but with *kubeletplugin.Device replaced by *CheckpointDevice.
// All other fields reference locally-owned types and are reused as-is.

type PreparedDeviceListV3 []PreparedDeviceV3
type PreparedDevicesV3 []*PreparedDeviceGroupV3

type PreparedDeviceV3 struct {
	Gpu  *PreparedGpuV3        `json:"gpu,omitempty"`
	Mig  *PreparedMigDeviceV3  `json:"mig,omitempty"`
	Vfio *PreparedVfioDeviceV3 `json:"vfio,omitempty"`
}

type PreparedGpuV3 struct {
	Info   *GpuInfo          `json:"info,omitempty"`
	Device *CheckpointDevice `json:"device,omitempty"`
}

type PreparedMigDeviceV3 struct {
	Concrete *MigLiveTuple     `json:"concrete,omitempty"`
	Device   *CheckpointDevice `json:"device,omitempty"`
}

type PreparedVfioDeviceV3 struct {
	Info   *VfioDeviceInfo   `json:"info,omitempty"`
	Device *CheckpointDevice `json:"device,omitempty"`
}

type PreparedDeviceGroupV3 struct {
	Devices     PreparedDeviceListV3 `json:"devices,omitempty"`
	ConfigState DeviceConfigState    `json:"configState,omitempty"`
}

// V3 claim/checkpoint envelope.

type CheckpointV3 struct {
	Checksum       checksum.Checksum     `json:"checksum"`
	PreparedClaims PreparedClaimsByUIDV3 `json:"preparedClaims,omitempty"`
}

type PreparedClaimsByUIDV3 map[string]PreparedClaimV3

type PreparedClaimV3 struct {
	CheckpointState ClaimCheckpointState            `json:"checkpointState,omitempty"`
	Status          resourceapi.ResourceClaimStatus `json:"status,omitempty"`
	PreparedDevices PreparedDevicesV3               `json:"preparedDevices,omitempty"`
	Name            string                          `json:"name,omitempty"`
	Namespace       string                          `json:"namespace,omitempty"`
}

// Conversion: in-memory PreparedDevices (from prepared.go, embedding
// *kubeletplugin.Device) <-> on-disk PreparedDevicesV3 (embedding
// *CheckpointDevice).

func preparedDevicesToV3(in PreparedDevices) PreparedDevicesV3 {
	if in == nil {
		return nil
	}
	out := make(PreparedDevicesV3, 0, len(in))
	for _, g := range in {
		if g == nil {
			out = append(out, nil)
			continue
		}
		gv3 := &PreparedDeviceGroupV3{
			Devices:     make(PreparedDeviceListV3, 0, len(g.Devices)),
			ConfigState: g.ConfigState,
		}
		for _, d := range g.Devices {
			gv3.Devices = append(gv3.Devices, preparedDeviceToV3(d))
		}
		out = append(out, gv3)
	}
	return out
}

func preparedDeviceToV3(d PreparedDevice) PreparedDeviceV3 {
	out := PreparedDeviceV3{}
	if d.Gpu != nil {
		out.Gpu = &PreparedGpuV3{
			Info:   d.Gpu.Info,
			Device: NewCheckpointDevice(d.Gpu.Device),
		}
	}
	if d.Mig != nil {
		out.Mig = &PreparedMigDeviceV3{
			Concrete: d.Mig.Concrete,
			Device:   NewCheckpointDevice(d.Mig.Device),
		}
	}
	if d.Vfio != nil {
		out.Vfio = &PreparedVfioDeviceV3{
			Info:   d.Vfio.Info,
			Device: NewCheckpointDevice(d.Vfio.Device),
		}
	}
	return out
}

func preparedDevicesFromV3(in PreparedDevicesV3) PreparedDevices {
	if in == nil {
		return nil
	}
	out := make(PreparedDevices, 0, len(in))
	for _, g := range in {
		if g == nil {
			out = append(out, nil)
			continue
		}
		group := &PreparedDeviceGroup{
			Devices:     make(PreparedDeviceList, 0, len(g.Devices)),
			ConfigState: g.ConfigState,
		}
		for _, d := range g.Devices {
			group.Devices = append(group.Devices, preparedDeviceFromV3(d))
		}
		out = append(out, group)
	}
	return out
}

func preparedDeviceFromV3(d PreparedDeviceV3) PreparedDevice {
	out := PreparedDevice{}
	if d.Gpu != nil {
		out.Gpu = &PreparedGpu{
			Info:   d.Gpu.Info,
			Device: d.Gpu.Device.ToKubeletPluginDevice(),
		}
	}
	if d.Mig != nil {
		out.Mig = &PreparedMigDevice{
			Concrete: d.Mig.Concrete,
			Device:   d.Mig.Device.ToKubeletPluginDevice(),
		}
	}
	if d.Vfio != nil {
		out.Vfio = &PreparedVfioDevice{
			Info:   d.Vfio.Info,
			Device: d.Vfio.Device.ToKubeletPluginDevice(),
		}
	}
	return out
}

// CheckpointV2 <-> CheckpointV3 conversion.

func (v2 *CheckpointV2) ToV3() *CheckpointV3 {
	v3 := &CheckpointV3{
		PreparedClaims: make(PreparedClaimsByUIDV3, len(v2.PreparedClaims)),
	}
	for uid, c := range v2.PreparedClaims {
		v3.PreparedClaims[uid] = PreparedClaimV3{
			CheckpointState: c.CheckpointState,
			Status:          c.Status,
			PreparedDevices: preparedDevicesToV3(c.PreparedDevices),
			Name:            c.Name,
			Namespace:       c.Namespace,
		}
	}
	return v3
}

func (v3 *CheckpointV3) ToV2() *CheckpointV2 {
	v2 := &CheckpointV2{
		PreparedClaims: make(PreparedClaimsByUIDV2, len(v3.PreparedClaims)),
	}
	for uid, c := range v3.PreparedClaims {
		v2.PreparedClaims[uid] = PreparedClaimV2{
			CheckpointState: c.CheckpointState,
			Status:          c.Status,
			PreparedDevices: preparedDevicesFromV3(c.PreparedDevices),
			Name:            c.Name,
			Namespace:       c.Namespace,
		}
	}
	return v2
}
