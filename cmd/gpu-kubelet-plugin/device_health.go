/*
 * Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
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
	FullGPUInstanceID uint32 = 0xFFFFFFFF
)

type nvmlDeviceHealthMonitor struct {
	nvmllib                  nvml.Interface
	eventSet                 nvml.EventSet
	unhealthy                chan *AllocatableDevice
	cancelContext            context.CancelFunc
	uuidToDeviceMap          map[string]*AllocatableDevice
	getDeviceByParentGiCiMap map[string]map[uint32]map[uint32]*AllocatableDevice
	wg                       sync.WaitGroup
}

func newNvmlDeviceHealthMonitor(ctx context.Context, config *Config, allocatable AllocatableDevices, nvdevlib *deviceLib) (*nvmlDeviceHealthMonitor, error) {
	if nvdevlib.nvmllib == nil {
		return nil, fmt.Errorf("nvml library is nil")
	}

	ctx, cancel := context.WithCancel(ctx)

	m := &nvmlDeviceHealthMonitor{
		nvmllib:       nvdevlib.nvmllib,
		unhealthy:     make(chan *AllocatableDevice, len(allocatable)),
		cancelContext: cancel,
	}

	if ret := m.nvmllib.Init(); ret != nvml.SUCCESS {
		cancel()
		return nil, fmt.Errorf("failed to initialize NVML: %v", ret)
	}

	klog.V(6).Info("creating NVML events for device health monitor")
	eventSet, ret := m.nvmllib.EventSetCreate()
	if ret != nvml.SUCCESS {
		_ = m.nvmllib.Shutdown()
		cancel()
		return nil, fmt.Errorf("failed to create event set: %w", ret)
	}
	m.eventSet = eventSet

	m.uuidToDeviceMap = getUUIDToDeviceMap(allocatable)

	m.getDeviceByParentGiCiMap = getDeviceByParentGiCiMap(allocatable)

	klog.V(6).Info("registering NVML events for device health monitor")
	m.registerEventsForDevices()

	skippedXids := m.xidsToSkip(config.flags.additionalXidsToIgnore)
	klog.V(6).Info("started device health monitoring")
	m.wg.Add(1)
	go m.run(ctx, skippedXids)

	return m, nil
}

func (m *nvmlDeviceHealthMonitor) registerEventsForDevices() {
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

func (m *nvmlDeviceHealthMonitor) Stop() {
	if m == nil {
		return
	}
	klog.V(6).Info("stopping health monitor")

	if m.cancelContext != nil {
		m.cancelContext()
	}

	m.wg.Wait()

	_ = m.eventSet.Free()

	if ret := m.nvmllib.Shutdown(); ret != nvml.SUCCESS {
		klog.Warningf("failed to shutdown NVML: %v", ret)
	}
	close(m.unhealthy)
}

func getUUIDToDeviceMap(allocatable AllocatableDevices) map[string]*AllocatableDevice {
	uuidToDeviceMap := make(map[string]*AllocatableDevice)

	for _, d := range allocatable {
		if u := d.UUID(); u != "" {
			uuidToDeviceMap[u] = d
		}
	}
	return uuidToDeviceMap
}

func (m *nvmlDeviceHealthMonitor) run(ctx context.Context, skippedXids map[uint64]bool) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
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
			pMap, ok1 := m.getDeviceByParentGiCiMap[eventUUID]
			if ok1 {
				giMap, ok2 := pMap[event.GpuInstanceId]
				if ok2 {
					affectedDevice = giMap[event.ComputeInstanceId]
				}
			}

			if affectedDevice == nil {
				klog.Infof("Ignoring event for unexpected device (UUID: %s, GI: %d, CI: %d)", eventUUID, event.GpuInstanceId, event.ComputeInstanceId)
				continue
			}

			klog.Infof("Sending unhealthy notification for device %s due to event type: %v and event data: %d", affectedDevice.UUID(), event.EventType, event.EventData)
			m.unhealthy <- affectedDevice
		}
	}
}

func (m *nvmlDeviceHealthMonitor) Unhealthy() <-chan *AllocatableDevice {
	return m.unhealthy
}

func getDeviceByParentGiCiMap(allocatable AllocatableDevices) map[string]map[uint32]map[uint32]*AllocatableDevice {
	deviceByParentGiCiMap := make(map[string]map[uint32]map[uint32]*AllocatableDevice)

	for _, d := range allocatable {
		var parentUUID string
		var giID, ciID uint32

		switch d.Type() {
		case GpuDeviceType:
			parentUUID = d.UUID()
			if parentUUID == "" {
				continue
			}
			giID = FullGPUInstanceID
			ciID = FullGPUInstanceID
		case MigDeviceType:
			parentUUID = d.Mig.parent.UUID
			if parentUUID == "" {
				continue
			}
			giID = d.Mig.giInfo.Id
			ciID = d.Mig.ciInfo.Id
		default:
			klog.Errorf("Skipping device with unknown type: %s", d.UUID())
			continue
		}

		if _, ok := deviceByParentGiCiMap[parentUUID]; !ok {
			deviceByParentGiCiMap[parentUUID] = make(map[uint32]map[uint32]*AllocatableDevice)
		}
		if _, ok := deviceByParentGiCiMap[parentUUID][giID]; !ok {
			deviceByParentGiCiMap[parentUUID][giID] = make(map[uint32]*AllocatableDevice)
		}
		deviceByParentGiCiMap[parentUUID][giID][ciID] = d
	}
	return deviceByParentGiCiMap
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

func (m *nvmlDeviceHealthMonitor) xidsToSkip(additionalXids string) map[uint64]bool {
	// Add the list of hardcoded disabled (ignored) XIDs:
	// http://docs.nvidia.com/deploy/xid-errors/index.html#topic_4
	// Application errors: the GPU should still be healthy.
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
