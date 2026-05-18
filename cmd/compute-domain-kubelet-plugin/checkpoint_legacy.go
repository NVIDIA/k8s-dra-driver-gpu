package main

import (
	"encoding/json"

	"k8s.io/dynamic-resource-allocation/kubeletplugin"
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
		channel.Device = &kubeletplugin.Device{
			Requests:     c.Device.RequestNames,
			PoolName:     c.Device.PoolName,
			DeviceName:   c.Device.DeviceName,
			CDIDeviceIDs: c.Device.CDIDeviceIDs,
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
		daemon.Device = &kubeletplugin.Device{
			Requests:     d.Device.RequestNames,
			PoolName:     d.Device.PoolName,
			DeviceName:   d.Device.DeviceName,
			CDIDeviceIDs: d.Device.CDIDeviceIDs,
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
	cpv1 := newCheckpoint()
	for k, v := range cp.V1.PreparedClaims {
		pds := make(PreparedDevices, 0, len(v))
		for _, pd := range v {
			pds = append(pds, pd.ToV1())
		}
		cpv1.V1.PreparedClaims[k] = PreparedClaim{
			PreparedDevices: pds,
		}
	}
	return cpv1
}
