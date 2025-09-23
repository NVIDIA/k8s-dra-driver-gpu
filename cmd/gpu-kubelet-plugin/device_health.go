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
	"strconv"
	"strings"
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

	if ret := m.nvmllib.Init(); ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to initialize NVML: %v", ret)
	}

	klog.V(6).Info("creating NVML events for device health monitor")
	eventSet, ret := m.nvmllib.EventSetCreate()
	if ret != nvml.SUCCESS {
		_ = m.nvmllib.Shutdown()
		return nil, fmt.Errorf("failed to create event set: %w", ret)
	}
	m.eventSet = eventSet

	m.uuidToDeviceMap = getUUIDToDeviceMap(allocatable)

	klog.V(6).Info("registering NVML events for device health monitor")
	m.registerDevicesForEvents()

	skippedXids := m.xidsToSkip(config.flags.additionalXidsToIgnore)
	klog.V(6).Info("started device health monitoring")
	m.wg.Add(1)
	go m.run(skippedXids)

	return m, nil
}

func (m *deviceHealthMonitor) registerDevicesForEvents() {
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
		gpu, ret := m.nvmllib.DeviceGetHandleByUUID(u)
		if ret != nvml.SUCCESS {
			klog.Infof("Unable to get device handle from UUID[%s]: %v; marking it as unhealthy", u, ret)
			m.unhealthy <- dev
			continue
		}

		supportedEvents, ret := gpu.GetSupportedEventTypes()
		if ret != nvml.SUCCESS {
			klog.Infof("unable to determine the supported events for %s: %v; marking it as unhealthy", u, ret)
			m.unhealthy <- dev
			continue
		}

		ret = gpu.RegisterEvents(eventMask&supportedEvents, m.eventSet)
		if ret == nvml.ERROR_NOT_SUPPORTED {
			klog.Warningf("Device %v is too old to support healthchecking.", u)
		}
		if ret != nvml.SUCCESS {
			klog.Infof("unable to register events for %s: %v; marking it as unhealthy", u, ret)
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

	if ret := m.nvmllib.Shutdown(); ret != nvml.SUCCESS {
		klog.Warningf("failed to shutdown NVML: %v", ret)
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

func (m *deviceHealthMonitor) run(skippedXids map[uint64]bool) {
	defer m.wg.Done()
	for {
		select {
		case <-m.stop:
			klog.V(6).Info("Stopping event-driven GPU health monitor...")
			return
		default:
			event, ret := m.eventSet.Wait(5000)
			if ret == nvml.ERROR_TIMEOUT {
				continue
			}
			if ret != nvml.SUCCESS {
				klog.Infof("Error waiting for event: %v; Marking all devices as unhealthy", ret)
				for _, dev := range m.uuidToDeviceMap {
					m.unhealthy <- dev
				}
				continue
			}

			if event.EventType != nvml.EventTypeXidCriticalError {
				klog.Infof("Skipping non-nvmlEventTypeXidCriticalError event: %+v", event)
				continue
			}

			if skippedXids[event.EventData] {
				klog.Infof("Skipping event %+v", event)
				continue
			}

			klog.Infof("Processing event %+v", event)
			eventUUID, ret := event.Device.GetUUID()
			if ret != nvml.SUCCESS {
				klog.Infof("Failed to determine uuid for event %v: %v; Marking all devices as unhealthy.", event, ret)
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

// getAdditionalXids returns a list of additional Xids to skip from the specified string.
// The input is treaded as a comma-separated string and all valid uint64 values are considered as Xid values.
// Invalid values nare ignored.
func getAdditionalXids(input string) []uint64 {
	if input == "" {
		return nil
	}

	var additionalXids []uint64
	klog.V(6).Infof("Creating a list of additional xids to ignore: [%s]", input)
	for _, additionalXid := range strings.Split(input, ",") {
		trimmed := strings.TrimSpace(additionalXid)
		if trimmed == "" {
			continue
		}
		xid, err := strconv.ParseUint(trimmed, 10, 64)
		if err != nil {
			klog.Infof("Ignoring malformed Xid value %v: %v", trimmed, err)
			continue
		}
		additionalXids = append(additionalXids, xid)
	}

	return additionalXids
}

func (m *deviceHealthMonitor) xidsToSkip(additionalXids string) map[uint64]bool {
	ignoredXids := []uint64{
		13,  // Graphics Engine Exception
		31,  // GPU memory page fault
		43,  // GPU stopped processing
		45,  // Preemptive cleanup, due to previous errors
		68,  // Video processor exception
		109, // Context Switch Timeout Error
	}

	skippedXids := make(map[uint64]bool)
	for _, id := range ignoredXids {
		skippedXids[id] = true
	}

	for _, additionalXid := range getAdditionalXids(additionalXids) {
		skippedXids[additionalXid] = true
	}
	return skippedXids
}
