/*
 * Copyright 2025 The Kubernetes Authors.
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
	"sync"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
	"k8s.io/klog/v2"
)

type deviceHealthMonitor struct {
	nvdevlib    *deviceLib
	allocatable AllocatableDevices
	eventSet    nvml.EventSet
	unhealthy   chan *AllocatableDevice
	stop        chan struct{}
	wg          sync.WaitGroup
}

func newDeviceHealthMonitor(ctx context.Context, config *Config, allocatable AllocatableDevices, nvdevlib *deviceLib) (*deviceHealthMonitor, error) {
	klog.Info("[SWATI DEBUG] initializing NVML..")
	if err := nvdevlib.Init(); err != nil {
		return nil, fmt.Errorf("failed to initialize NVML: %w", err)
	}
	//defer nvdevlib.alwaysShutdown()

	//klog.Info("[SWATI DEBUG] getting all devices..")
	//allocatable, err := nvdevlib.enumerateAllPossibleDevices(config)
	//if err != nil {
	//	return nil, fmt.Errorf("error enumerating all possible devices: %w", err)
	//}

	klog.Info("[SWATI DEBUG] creating NVML events")
	eventSet, err := nvdevlib.nvmllib.EventSetCreate()
	if err != nvml.SUCCESS {
		return nil, fmt.Errorf("failed to create event set: %w", err)
	}

	monitor := &deviceHealthMonitor{
		nvdevlib:    nvdevlib,
		allocatable: allocatable,
		eventSet:    eventSet,
		unhealthy:   make(chan *AllocatableDevice, len(allocatable)),
		stop:        make(chan struct{}),
	}

	klog.Info("[SWATI DEBUG] registering NVML events")
	if err := monitor.registerDevicesForEvents(); err != nil {
		monitor.eventSet.Free()
		return nil, fmt.Errorf("failed to register devices for health monitoring: %w", err)
	}

	monitor.start()
	return monitor, nil
}

func (m *deviceHealthMonitor) registerDevicesForEvents() error {
	nvmllib := m.nvdevlib.nvmllib
	eventMask := uint64(nvml.EventTypeXidCriticalError | nvml.EventTypeDoubleBitEccError | nvml.EventTypeSingleBitEccError)

	for _, uuid := range m.allocatable.UUIDs() {
		gpu, err := nvmllib.DeviceGetHandleByUUID(uuid)
		if err != nvml.SUCCESS {
			klog.Infof("Unable to get NVML handle for UUID %s: %v; skipping health check for this device", uuid, err)
			continue
		}

		if err := gpu.RegisterEvents(eventMask, m.eventSet); err != nvml.SUCCESS {
			klog.Infof("Failed to register events for device %s: %v; skipping health check for this device", uuid, err)
		}
	}
	return nil
}

func (m *deviceHealthMonitor) start() {
	klog.Info("[SWATI DEBUG] starting health monitor")
	m.wg.Add(1)
	go m.run()
}

func (m *deviceHealthMonitor) Stop() {
	if m == nil {
		return
	}
	klog.Info("[SWATI DEBUG] stopping health monitor")
	close(m.stop)
	m.wg.Wait()
	close(m.unhealthy)
	m.eventSet.Free()

	if m.nvdevlib != nil {
		m.nvdevlib.alwaysShutdown()
	}
}

func (m *deviceHealthMonitor) run() {
	defer m.wg.Done()

	uuidToDeviceMap := make(map[string]*AllocatableDevice)
	for _, device := range m.allocatable {
		uuid := device.GetUUID()
		if uuid != "" {
			uuidToDeviceMap[uuid] = device
		}
	}

	klog.Info("Starting event-driven GPU health monitor...")

	for {
		select {
		case <-m.stop:
			klog.Info("Stopping event-driven GPU health monitor...")
			return
		default:
			event, err := m.eventSet.Wait(5000)
			if err == nvml.ERROR_TIMEOUT {
				klog.Info("[SWATI DEBUG] timedout")
				continue
			}
			if err != nvml.SUCCESS {
				klog.Infof("Error waiting for event: %v; Marking all devices as unhealthy", err)
				for _, dev := range m.allocatable {
					m.unhealthy <- dev
				}
				continue
			}

			// Process health events
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
				for _, dev := range m.allocatable {
					m.unhealthy <- dev
				}
				continue
			}

			device, exists := uuidToDeviceMap[eventUUID]
			if !exists {
				continue
			}

			// Send notification to driver
			klog.Infof("Sending unhealthy notification for device %s due to event type %v", eventUUID, event.EventType)
			select {
			case m.unhealthy <- device:
				// Successfully sent notification
			default:
				// Channel full, log and continue
				klog.Warningf("Health notification channel full, dropping event for device %s", eventUUID)
			}
		}
	}
}

func (m *deviceHealthMonitor) Unhealthy() <-chan *AllocatableDevice {
	return m.unhealthy
}
