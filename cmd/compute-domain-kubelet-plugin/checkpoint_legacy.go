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
	"encoding/json"

	drapbv1 "k8s.io/kubelet/pkg/apis/dra/v1beta1"
	"k8s.io/kubernetes/pkg/kubelet/checkpointmanager/checksum"
)

// Legacy structs and methods to help with conversion as necessary.
type PreparedDeviceList2503RC2 []PreparedDevice2503RC2
type PreparedDevices2503RC2 []*PreparedDeviceGroup2503RC2
type PreparedClaims2503RC2 map[string]PreparedDevices2503RC2

type PreparedDevice2503RC2 struct {
	Channel *PreparedComputeDomainChannel2503RC2 `json:"channel"`
	Daemon  *PreparedComputeDomainDaemon2503RC2  `json:"daemon"`
}

type PreparedComputeDomainChannel2503RC2 struct {
	Info   *ComputeDomainChannelInfo `json:"info"`
	Device *drapbv1.Device           `json:"device"`
}

type PreparedComputeDomainDaemon2503RC2 struct {
	Info   *ComputeDomainDaemonInfo `json:"info"`
	Device *drapbv1.Device          `json:"device"`
}

type PreparedDeviceGroup2503RC2 struct {
	Devices     PreparedDeviceList2503RC2 `json:"devices"`
	ConfigState DeviceConfigState         `json:"configState"`
}

type Checkpoint2503RC2 struct {
	Checksum checksum.Checksum    `json:"checksum"`
	V1       *Checkpoint2503RC2V1 `json:"v1,omitempty"`
}

type Checkpoint2503RC2V1 struct {
	PreparedClaims PreparedClaims2503RC2 `json:"preparedClaims,omitempty"`
}

func (cp *Checkpoint2503RC2) MarshalCheckpoint() ([]byte, error) {
	cp.Checksum = 0
	out, err := json.Marshal(*cp)
	if err != nil {
		return nil, err
	}
	cp.Checksum = checksum.New(out)
	return json.Marshal(*cp)
}

func (cp *Checkpoint2503RC2) UnmarshalCheckpoint(data []byte) error {
	return json.Unmarshal(data, cp)
}

func (cp *Checkpoint2503RC2) VerifyChecksum() error {
	ck := cp.Checksum
	cp.Checksum = 0
	defer func() {
		cp.Checksum = ck
	}()
	out, err := json.Marshal(*cp)
	if err != nil {
		return err
	}
	return ck.Verify(out)
}

// ToV1 converts a PreparedDevice2503RC2 to a PreparedDevice.
func (d *PreparedDevice2503RC2) ToV1() PreparedDevice {
	device := PreparedDevice{}
	if d.Channel != nil {
		device.Channel = d.Channel.ToV1()
	}
	if d.Daemon != nil {
		device.Daemon = d.Daemon.ToV1()
	}
	return device
}

// ToV1 converts a PreparedComputeDomainChannel2503RC2 to a PreparedComputeDomainChannel.
func (c *PreparedComputeDomainChannel2503RC2) ToV1() *PreparedComputeDomainChannel {
	channel := &PreparedComputeDomainChannel{}
	if c.Info != nil {
		channel.Info = c.Info
	}
	if c.Device != nil {
		channel.Device = &CheckpointedDevice{
			Requests:     c.Device.RequestNames,
			PoolName:     c.Device.PoolName,
			DeviceName:   c.Device.DeviceName,
			CDIDeviceIDs: c.Device.CdiDeviceIds,
		}
	}
	return channel
}

// ToV1 converts a PreparedComputeDomainDaemon2503RC2 to a PreparedComputeDomainDaemon.
func (d *PreparedComputeDomainDaemon2503RC2) ToV1() *PreparedComputeDomainDaemon {
	daemon := &PreparedComputeDomainDaemon{}
	if d.Info != nil {
		daemon.Info = d.Info
	}
	if d.Device != nil {
		daemon.Device = &CheckpointedDevice{
			Requests:     d.Device.RequestNames,
			PoolName:     d.Device.PoolName,
			DeviceName:   d.Device.DeviceName,
			CDIDeviceIDs: d.Device.CdiDeviceIds,
		}
	}
	return daemon
}

// ToV1 converts a PreparedDeviceGroup2503RC2 to a PreparedDeviceGroup.
func (g *PreparedDeviceGroup2503RC2) ToV1() *PreparedDeviceGroup {
	group := &PreparedDeviceGroup{
		Devices:     make(PreparedDeviceList, 0, len(g.Devices)),
		ConfigState: g.ConfigState,
	}
	for _, d := range g.Devices {
		group.Devices = append(group.Devices, d.ToV1())
	}
	return group
}

// ToV1 converts a Checkpoint2503RC2 to a Checkpoint.
func (cp *Checkpoint2503RC2) ToV1() *Checkpoint {
	cpv1 := &Checkpoint{
		V1: &CheckpointV1{
			PreparedClaims: make(PreparedClaimsByUIDV1),
		},
	}
	for k, v := range cp.V1.PreparedClaims {
		pds := make(PreparedDevices, 0, len(v))
		for _, pd := range v {
			pds = append(pds, pd.ToV1())
		}
		cpv1.V1.PreparedClaims[k] = PreparedClaimV1{
			PreparedDevices: pds,
		}
	}
	return cpv1
}
