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
	"fmt"
	"sync"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"k8s.io/klog/v2"
)

const (
	InstanceID uint32 = 0xFFFFFFFF
)

type deviceHealthMonitor struct {
	nvmllib         nvml.Interface
	eventSet        nvml.EventSet
	unhealthy       chan *AllocatableDevice
	stop            chan struct{}
	uuidToDeviceMap map[string]*AllocatableDevice
	wg              sync.WaitGroup
}

func newDeviceHealthMonitor(ctx context.Context, config *Config, allocatable AllocatableDevices, nvdevlib *deviceLib) (*deviceHealthMonitor, error) {
	if nvdevlib.nvmllib == nil {
		return nil, fmt.Errorf("nvml library is nil")
	}

	m := &deviceHealthMonitor{
		nvmllib:   nvdevlib.nvmllib,
		unhealthy: make(chan *AllocatableDevice, len(allocatable)),
		stop:      make(chan struct{}),
	}

	if r := m.nvmllib.Init(); r != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to initialize NVML: %v", r)
	}

	klog.V(6).Info("creating NVML events for device health monitor")
	eventSet, err := m.nvmllib.EventSetCreate()
	if err != nvml.SUCCESS {
		_ = m.nvmllib.Shutdown()
		return nil, fmt.Errorf("failed to create event set: %w", err)
	}
	m.eventSet = eventSet

	m.uuidToDeviceMap = getUUIDToDeviceMap(allocatable)

	klog.V(6).Info("registering NVML events for device health monitor")
	m.registerDevicesForEvents()

	klog.V(6).Info("started device health monitoring")
	m.wg.Add(1)
	go m.run()

	return m, nil
}

func (m *deviceHealthMonitor) registerDevicesForEvents() {
	// TODO: add a list of xids to ignore
	eventMask := uint64(nvml.EventTypeXidCriticalError | nvml.EventTypeDoubleBitEccError | nvml.EventTypeSingleBitEccError)

	processedUUIDs := make(map[string]bool)

	for uuid, dev := range m.uuidToDeviceMap {
		var u string
		if dev.Type() == MigDeviceType {
			u = dev.Mig.parent.UUID
		} else {
			u = uuid
		}

		if processedUUIDs[u] {
			continue
		}
		gpu, err := m.nvmllib.DeviceGetHandleByUUID(u)
		if err != nvml.SUCCESS {
			klog.Infof("Unable to get device handle from UUID[%s]: %v; marking it as unhealthy", u, err)
			m.unhealthy <- dev
			continue
		}

		supportedEvents, err := gpu.GetSupportedEventTypes()
		if err != nvml.SUCCESS {
			klog.Infof("unable to determine the supported events for %s: %v; marking it as unhealthy", u, err)
			m.unhealthy <- dev
			continue
		}

		err = gpu.RegisterEvents(eventMask&supportedEvents, m.eventSet)
		if err == nvml.ERROR_NOT_SUPPORTED {
			klog.Warningf("Device %v is too old to support healthchecking.", u)
		}
		if err != nvml.SUCCESS {
			klog.Infof("unable to register events for %s: %v; marking it as unhealthy", u, err)
			m.unhealthy <- dev
		}
		processedUUIDs[u] = true
	}
}

func (m *deviceHealthMonitor) Stop() {
	if m == nil {
		return
	}
	klog.V(6).Info("stopping health monitor")

	close(m.stop)
	m.wg.Wait()

	m.eventSet.Free()

	if r := m.nvmllib.Shutdown(); r != nvml.SUCCESS {
		klog.Warningf("failed to shutdown NVML: %v", r)
	}
	close(m.unhealthy)
}

func getUUIDToDeviceMap(allocatable AllocatableDevices) map[string]*AllocatableDevice {
	uuidToDeviceMap := make(map[string]*AllocatableDevice)

	for _, d := range allocatable {
		if u := d.GetUUID(); u != "" {
			uuidToDeviceMap[u] = d
		}
	}
	return uuidToDeviceMap
}

func (m *deviceHealthMonitor) run() {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			klog.V(6).Info("Stopping event-driven GPU health monitor...")
			return
		default:
			event, err := m.eventSet.Wait(5000)
			if err == nvml.ERROR_TIMEOUT {
				continue
			}
			if err != nvml.SUCCESS {
				klog.Infof("Error waiting for event: %v; Marking all devices as unhealthy", err)
				for _, dev := range m.uuidToDeviceMap {
					m.unhealthy <- dev
				}
				continue
			}

			klog.Infof("Processing event %+v", event)
			switch event.EventType {
			case nvml.EventTypeXidCriticalError:
				klog.Warningf("Critical XID error detected on device: %+v", event)
			case nvml.EventTypeDoubleBitEccError:
				klog.Warningf("Double-bit ECC error detected on device: %+v", event)
			case nvml.EventTypeSingleBitEccError:
				klog.Infof("Single-bit ECC error detected on device:%+v", event)
			default:
				continue
			}

			eventUUID, err := event.Device.GetUUID()
			if err != nvml.SUCCESS {
				klog.Infof("Failed to determine uuid for event %v: %v; Marking all devices as unhealthy.", event, err)
				for _, dev := range m.uuidToDeviceMap {
					m.unhealthy <- dev
				}
				continue
			}

			var affectedDevice *AllocatableDevice
			if event.GpuInstanceId != InstanceID && event.ComputeInstanceId != InstanceID {
				affectedDevice = m.findMigDevice(eventUUID, event.GpuInstanceId, event.ComputeInstanceId)
				klog.Infof("Event for mig device: %v", affectedDevice)
			} else {
				affectedDevice = m.findGpuDevice(eventUUID)
			}

			if affectedDevice == nil {
				klog.Infof("Ignoring event for unexpected device (UUID: %s, GI: %d, CI: %d)", eventUUID, event.GpuInstanceId, event.ComputeInstanceId)
				continue
			}
			klog.Infof("Sending unhealthy notification for device %s due to event type %v", eventUUID, event.EventType)
			m.unhealthy <- affectedDevice
		}
	}
}

func (m *deviceHealthMonitor) Unhealthy() <-chan *AllocatableDevice {
	return m.unhealthy
}

func (m *deviceHealthMonitor) findMigDevice(parentUUID string, giID uint32, ciID uint32) *AllocatableDevice {
	for _, device := range m.uuidToDeviceMap {
		if device.Type() != MigDeviceType {
			continue
		}

		if device.Mig.parent.UUID == parentUUID &&
			device.Mig.giInfo.Id == giID &&
			device.Mig.ciInfo.Id == ciID {
			return device
		}
	}
	return nil
}

func (m *deviceHealthMonitor) findGpuDevice(uuid string) *AllocatableDevice {
	device, exists := m.uuidToDeviceMap[uuid]
	if exists && device.Type() == GpuDeviceType {
		return device
	}
	return nil
}
