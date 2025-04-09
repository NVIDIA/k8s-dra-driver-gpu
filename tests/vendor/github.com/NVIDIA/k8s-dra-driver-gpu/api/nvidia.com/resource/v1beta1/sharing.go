/*
 * Copyright (c) 2023, NVIDIA CORPORATION.  All rights reserved.
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

package v1beta1

import (
	"errors"
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/api/resource"
)

// These constants represent the different Sharing strategies.
const (
	TimeSlicingStrategy = "TimeSlicing"
	MpsStrategy         = "MPS"
)

// These constants represent the different TimeSlicing configurations.
const (
	DefaultTimeSlice TimeSliceInterval = "Default"
	ShortTimeSlice   TimeSliceInterval = "Short"
	MediumTimeSlice  TimeSliceInterval = "Medium"
	LongTimeSlice    TimeSliceInterval = "Long"
)

// Sharing provides methods to check if a given sharing strategy is selected and grab its configuration.
// +k8s:deepcopy-gen=false
type Sharing interface {
	IsTimeSlicing() bool
	IsMps() bool
	GetTimeSlicingConfig() (*TimeSlicingConfig, error)
	GetMpsConfig() (*MpsConfig, error)
}

// GpuSharingStrategy encodes the valid Sharing strategies as a string.
type GpuSharingStrategy string

// MigDeviceSharingStrategy encodes the valid Sharing strategies as a string.
type MigDeviceSharingStrategy string

// TimeSliceInterval encodes the valid timeslice duration as a string.
type TimeSliceInterval string

// MpsPerDevicePinnedMemoryLimit holds the string representation of the limits across multiple devices.
type MpsPerDevicePinnedMemoryLimit map[string]resource.Quantity

// GpuSharing holds the current sharing strategy for GPUs and its settings.
type GpuSharing struct {
	Strategy          GpuSharingStrategy `json:"strategy"`
	TimeSlicingConfig *TimeSlicingConfig `json:"timeSlicingConfig,omitempty"`
	MpsConfig         *MpsConfig         `json:"mpsConfig,omitempty"`
}

// MigDeviceSharing holds the current sharing strategy for MIG Devices and its settings.
type MigDeviceSharing struct {
	Strategy  GpuSharingStrategy `json:"strategy"`
	MpsConfig *MpsConfig         `json:"mpsConfig,omitempty"`
}

// TimeSlicingSettings provides the settings for CUDA time-slicing.
type TimeSlicingConfig struct {
	Interval *TimeSliceInterval `json:"interval,omitempty"`
}

// MpsConfig provides the configuring for an MPS control daemon.
type MpsConfig struct {
	DefaultActiveThreadPercentage *int `json:"defaultActiveThreadPercentage,omitempty"`
	// DefaultPinnedDeviceMemoryLimit represents the pinned memory limit to be applied for all devices.
	// This can be overridden for specific devices by specifying an associated entry DefaultPerDevicePinnedMemoryLimit for the device.
	DefaultPinnedDeviceMemoryLimit *resource.Quantity `json:"defaultPinnedDeviceMemoryLimit,omitempty"`
	// DefaultPerDevicePinnedMemoryLimit represents the pinned memory limit per device associated with an MPS daemon.
	// This is defined as a map of device index or UUI to a memory limit and overrides a setting applied using DefaultPinnedDeviceMemoryLimit.
	DefaultPerDevicePinnedMemoryLimit MpsPerDevicePinnedMemoryLimit `json:"defaultPerDevicePinnedMemoryLimit,omitempty"`
}

// IsTimeSlicing checks if the TimeSlicing strategy is applied.
func (s *GpuSharing) IsTimeSlicing() bool {
	if s == nil {
		return false
	}
	return s.Strategy == TimeSlicingStrategy
}

// IsMps checks if the MPS strategy is applied.
func (s *GpuSharing) IsMps() bool {
	if s == nil {
		return false
	}
	return s.Strategy == MpsStrategy
}

// IsTimeSlicing checks if the TimeSlicing strategy is applied.
func (s *MigDeviceSharing) IsTimeSlicing() bool {
	if s == nil {
		return false
	}
	return s.Strategy == TimeSlicingStrategy
}

// IsMps checks if the MPS strategy is applied.
func (s *MigDeviceSharing) IsMps() bool {
	if s == nil {
		return false
	}
	return s.Strategy == MpsStrategy
}

// GetTimeSlicingConfig returns the timeslicing config that applies to the given strategy.
func (s *GpuSharing) GetTimeSlicingConfig() (*TimeSlicingConfig, error) {
	if s == nil {
		return nil, fmt.Errorf("no sharing set to get config from")
	}
	if s.Strategy != TimeSlicingStrategy {
		return nil, fmt.Errorf("strategy is not set to '%v'", TimeSlicingStrategy)
	}
	if s.MpsConfig != nil {
		return nil, fmt.Errorf("cannot use MpsConfig with the '%v' strategy", TimeSlicingStrategy)
	}
	return s.TimeSlicingConfig, nil
}

// GetTimeSlicingConfig returns the timeslicing config that applies to the given strategy.
func (s *MigDeviceSharing) GetTimeSlicingConfig() (*TimeSlicingConfig, error) {
	return nil, nil
}

// GetMpsConfig returns the MPS config that applies to the given strategy.
func (s *GpuSharing) GetMpsConfig() (*MpsConfig, error) {
	if s == nil {
		return nil, fmt.Errorf("no sharing set to get config from")
	}
	if s.Strategy != MpsStrategy {
		return nil, fmt.Errorf("strategy is not set to '%v'", MpsStrategy)
	}
	if s.TimeSlicingConfig != nil {
		return nil, fmt.Errorf("cannot use TimeSlicingConfig with the '%v' strategy", MpsStrategy)
	}
	return s.MpsConfig, nil
}

// GetMpsConfig returns the MPS config that applies to the given strategy.
func (s *MigDeviceSharing) GetMpsConfig() (*MpsConfig, error) {
	if s == nil {
		return nil, fmt.Errorf("no sharing set to get config from")
	}
	if s.Strategy != MpsStrategy {
		return nil, fmt.Errorf("strategy is not set to '%v'", MpsStrategy)
	}
	return s.MpsConfig, nil
}

// Int returns the integer representations of a timeslice duration.
func (t TimeSliceInterval) Int() int {
	switch t {
	case DefaultTimeSlice:
		return 0
	case ShortTimeSlice:
		return 1
	case MediumTimeSlice:
		return 2
	case LongTimeSlice:
		return 3
	}
	return -1
}

// ErrInvalidDeviceSelector indicates that a device index or UUID was invalid.
var ErrInvalidDeviceSelector error = errors.New("invalid device")

// ErrInvalidLimit indicates that a limit was invalid.
var ErrInvalidLimit error = errors.New("invalid limit")

// Normalize converts the specified per-device pinned memory limits to limits for the devices that are to be allocated.
// If provided, the defaultPinnedDeviceMemoryLimit is applied to each device before being overridden by specific values.
func (m MpsPerDevicePinnedMemoryLimit) Normalize(uuids []string, defaultPinnedDeviceMemoryLimit *resource.Quantity) (map[string]string, error) {
	limits, err := (*limit)(defaultPinnedDeviceMemoryLimit).get(uuids)
	if err != nil {
		return nil, err
	}

	devices := newUUIDSet(uuids)
	for k, v := range m {
		id, err := devices.Normalize(k)
		if err != nil {
			return nil, err
		}
		megabyte, valid := (limit)(v).Megabyte()
		if !valid {
			return nil, fmt.Errorf("%w: value set too low: %v: %v", ErrInvalidLimit, k, v)
		}
		limits[id] = megabyte
	}
	return limits, nil
}

type limit resource.Quantity

func (d *limit) get(uuids []string) (map[string]string, error) {
	limits := make(map[string]string)
	if d == nil || len(uuids) == 0 {
		return limits, nil
	}

	megabyte, valid := d.Megabyte()
	if !valid {
		return nil, fmt.Errorf("%w: default value set too low: %v", ErrInvalidLimit, d)
	}
	for _, uuid := range uuids {
		limits[uuid] = megabyte
	}

	return limits, nil
}

func (d limit) Value() int64 {
	return (*resource.Quantity)(&d).Value()
}

func (d limit) Megabyte() (string, bool) {
	v := d.Value() / 1024 / 1024
	return fmt.Sprintf("%vM", v), v > 0
}

type uuidSet struct {
	uuids  []string
	lookup map[string]bool
}

// newUUIDSet creates a set of UUIDs for managing pinned memory for requested devices.
func newUUIDSet(uuids []string) *uuidSet {
	lookup := make(map[string]bool)
	for _, uuid := range uuids {
		lookup[uuid] = true
	}

	return &uuidSet{
		uuids:  uuids,
		lookup: lookup,
	}
}

func (s *uuidSet) Normalize(key string) (string, error) {
	// Check whether key is a UUID
	if _, ok := s.lookup[key]; ok {
		return key, nil
	}

	index, err := strconv.Atoi(key)
	if err != nil {
		return "", fmt.Errorf("%w: unable to parse key as an integer: %v", ErrInvalidDeviceSelector, key)
	}

	if index >= 0 && index < len(s.uuids) {
		return s.uuids[index], nil
	}

	return "", fmt.Errorf("%w: invalid device index: %v", ErrInvalidDeviceSelector, index)
}
