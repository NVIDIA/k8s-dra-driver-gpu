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

// For a MIG device the placement is defined by the 3-tuple <parent UUID, GI, CI>.
// For a full device the returned 3-tuple is the device's uuid and (FullGPUInstanceID) 0xFFFFFFFF for the other two elements.
type devicePlacementMap map[string]map[uint32]map[uint32]*AllocatableDevice

type nvmlDeviceHealthMonitor struct {
	nvmllib           nvml.Interface
	eventSet          nvml.EventSet
	unhealthy         chan *AllocatableDevice
	cancelContext     context.CancelFunc
	deviceByPlacement devicePlacementMap
	skippedXids       map[uint64]bool
	wg                sync.WaitGroup
}

func newNvmlDeviceHealthMonitor(config *Config, allocatable AllocatableDevices, nvdevlib *deviceLib) (*nvmlDeviceHealthMonitor, error) {
	if nvdevlib.nvmllib == nil {
		return nil, fmt.Errorf("nvml library is nil")
	}
	if ret := nvdevlib.nvmllib.Init(); ret != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to initialize NVML: %v", ret)
	}
	defer func() {
		_ = nvdevlib.nvmllib.Shutdown()
	}()

	m := &nvmlDeviceHealthMonitor{
		nvmllib:           nvdevlib.nvmllib,
		unhealthy:         make(chan *AllocatableDevice, len(allocatable)),
		deviceByPlacement: getDevicePlacementMap(allocatable),
		skippedXids:       xidsToSkip(config.flags.additionalXidsToIgnore),
	}
	return m, nil
}

func (m *nvmlDeviceHealthMonitor) Start(ctx context.Context) (rerr error) {
	if ret := m.nvmllib.Init(); ret != nvml.SUCCESS {
		return fmt.Errorf("failed to initialize NVML: %v", ret)
	}

	defer func() {
		if rerr != nil {
			_ = m.nvmllib.Shutdown()
		}
	}()

	klog.V(6).Info("creating NVML events for device health monitor")
	eventSet, ret := m.nvmllib.EventSetCreate()
	if ret != nvml.SUCCESS {
		return fmt.Errorf("failed to create event set: %w", ret)
	}

	ctx, cancel := context.WithCancel(ctx)
	m.cancelContext = cancel

	m.eventSet = eventSet

	klog.V(6).Info("registering NVML events for device health monitor")
	m.registerEventsForDevices()

	klog.V(6).Info("started device health monitoring")
	m.wg.Add(1)
	go m.run(ctx)

	return nil
}

func (m *nvmlDeviceHealthMonitor) registerEventsForDevices() {
	eventMask := uint64(nvml.EventTypeXidCriticalError | nvml.EventTypeDoubleBitEccError | nvml.EventTypeSingleBitEccError)

	for parentUUID, giMap := range m.deviceByPlacement {
		gpu, ret := m.nvmllib.DeviceGetHandleByUUID(parentUUID)
		if ret != nvml.SUCCESS {
			klog.Infof("Unable to get device handle from UUID[%s]: %v; marking it as unhealthy", parentUUID, ret)
			m.markAllSlicesUnhealthy(giMap)
			continue
		}

		supportedEvents, ret := gpu.GetSupportedEventTypes()
		if ret != nvml.SUCCESS {
			klog.Infof("unable to determine the supported events for %s: %v; marking it as unhealthy", parentUUID, ret)
			m.markAllSlicesUnhealthy(giMap)
			continue
		}

		ret = gpu.RegisterEvents(eventMask&supportedEvents, m.eventSet)
		if ret == nvml.ERROR_NOT_SUPPORTED {
			klog.Warningf("Device %v is too old to support healthchecking.", parentUUID)
		}
		if ret != nvml.SUCCESS {
			klog.Infof("unable to register events for %s: %v; marking it as unhealthy", parentUUID, ret)
			m.markAllSlicesUnhealthy(giMap)
		}
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

	if ret := m.eventSet.Free(); ret != nvml.SUCCESS {
		klog.Warningf("failed to unset events: %v", ret)
	}

	if ret := m.nvmllib.Shutdown(); ret != nvml.SUCCESS {
		klog.Warningf("failed to shutdown NVML: %v", ret)
	}
	close(m.unhealthy)
}

func (m *nvmlDeviceHealthMonitor) run(ctx context.Context) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			klog.V(6).Info("Stopping event-driven GPU health monitor...")
			return
		default:
			event, ret := m.eventSet.Wait(5000) // timeout in 5000 ms.
			if ret == nvml.ERROR_TIMEOUT {
				continue
			}
			// not all return errors are handled as currently there is no proper way to process these errors other than marking all devices healthy.
			// Ref doc: [https://docs.nvidia.com/deploy/nvml-api/group__nvmlEvents.html#group__nvmlEvents_1g9714b0ca9a34c7a7780f87fee16b205c].
			if ret != nvml.SUCCESS {
				if ret == nvml.ERROR_GPU_IS_LOST {
					klog.Infof("GPU is lost error: %v; Marking all devices as unhealthy", ret)
					m.markAllDevicesUnhealthy()
					continue
				}
				klog.Warningf("Error waiting for NVML event: %v. Retrying...", ret)
				continue
			}

			// TODO: check why other supported types are not considered?
			eType := event.EventType
			eData := event.EventData
			eGI := event.GpuInstanceId
			eCI := event.ComputeInstanceId
			if eType != nvml.EventTypeXidCriticalError {
				klog.Infof("Skipping non-nvmlEventTypeXidCriticalError event: Data=%d, Type=%d, GI=%d, CI=%d", eData, eType, eGI, eCI)
				continue
			}

			if m.skippedXids[eData] {
				klog.Infof("Skipping XID event: Data=%d, Type=%d, GI=%d, CI=%d", eData, eType, eGI, eCI)
				continue
			}

			klog.Infof("Processing event XID=%d event", eData)
			// this seems an extreme action.
			// should we just log the error and proceed anyway.
			// TODO: look into how to properly handle this error.
			eventUUID, ret := event.Device.GetUUID()
			if ret != nvml.SUCCESS {
				klog.Infof("Failed to determine uuid for event %v: %v; Marking all devices as unhealthy.", event, ret)
				m.markAllDevicesUnhealthy()
				continue
			}
			affectedDevice := m.deviceByPlacement.get(eventUUID, eGI, eCI)
			if affectedDevice == nil {
				klog.Infof("Ignoring event for unexpected device (UUID:%s, GI:%d, CI:%d)", eventUUID, eGI, eCI)
				continue
			}

			klog.Infof("Sending unhealthy notification for device %s due to event type:%v and event data:%d", affectedDevice.UUID(), eType, eData)
			m.unhealthy <- affectedDevice
		}
	}
}

func (m *nvmlDeviceHealthMonitor) Unhealthy() <-chan *AllocatableDevice {
	return m.unhealthy
}

func (m *nvmlDeviceHealthMonitor) markAllDevicesUnhealthy() {
	for _, giMap := range m.deviceByPlacement {
		m.markAllSlicesUnhealthy(giMap)
	}
}

// markAllSlicesUnhealthy is a helper function to mark every mig device under a parent as unhealthy.
func (m *nvmlDeviceHealthMonitor) markAllSlicesUnhealthy(giMap map[uint32]map[uint32]*AllocatableDevice) {
	for _, ciMap := range giMap {
		for _, dev := range ciMap {
			// Non-blocking send to avoid deadlocks if channel is full.
			select {
			case m.unhealthy <- dev:
				klog.V(6).Infof("Marked device %s as unhealthy", dev.UUID())
			default:
				klog.Errorf("Unhealthy channel full. Dropping unhealthy notification for device %s", dev.UUID())
			}
		}
	}
}

func getDevicePlacementMap(allocatable AllocatableDevices) devicePlacementMap {
	placementMap := make(devicePlacementMap)

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
			klog.Errorf("Skipping device with unknown type: %s", d.Type())
			continue
		}
		placementMap.addDevice(parentUUID, giID, ciID, d)
	}
	return placementMap
}

func (p devicePlacementMap) addDevice(parentUUID string, giID uint32, ciID uint32, d *AllocatableDevice) {
	if _, ok := p[parentUUID]; !ok {
		p[parentUUID] = make(map[uint32]map[uint32]*AllocatableDevice)
	}
	if _, ok := p[parentUUID][giID]; !ok {
		p[parentUUID][giID] = make(map[uint32]*AllocatableDevice)
	}
	p[parentUUID][giID][ciID] = d
}

func (p devicePlacementMap) get(uuid string, gi, ci uint32) *AllocatableDevice {
	giMap, ok := p[uuid]
	if !ok {
		return nil
	}

	ciMap, ok := giMap[gi]
	if !ok {
		return nil
	}
	return ciMap[ci]
}

// getAdditionalXids returns a list of additional Xids to skip from the specified string.
// The input is treaded as a comma-separated string and all valid uint64 values are considered as Xid values.
// Invalid values nare ignored.
// TODO: add list of EXPLICIT XIDs from [https://github.com/NVIDIA/k8s-device-plugin/pull/1443].
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

func xidsToSkip(additionalXids string) map[uint64]bool {
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
