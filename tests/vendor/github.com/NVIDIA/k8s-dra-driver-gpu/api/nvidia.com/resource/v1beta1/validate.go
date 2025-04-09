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

package v1beta1

import (
	"fmt"
)

// Validate ensures that GpuSharingStrategy has a valid set of values.
func (s GpuSharingStrategy) Validate() error {
	switch s {
	case TimeSlicingStrategy, MpsStrategy:
		return nil
	}
	return fmt.Errorf("unknown GPU sharing strategy: %v", s)
}

// Validate ensures that MigDeviceSharingStrategy has a valid set of values.
func (s MigDeviceSharingStrategy) Validate() error {
	switch s {
	case TimeSlicingStrategy, MpsStrategy:
		return nil
	}
	return fmt.Errorf("unknown GPU sharing strategy: %v", s)
}

// Validate ensures that TimeSliceInterval has a valid set of values.
func (t TimeSliceInterval) Validate() error {
	switch t {
	case DefaultTimeSlice, ShortTimeSlice, MediumTimeSlice, LongTimeSlice:
		return nil
	}
	return fmt.Errorf("unknown time-slice interval: %v", t)
}

// Validate ensures that TimeSlicingConfig has a valid set of values.
func (c *TimeSlicingConfig) Validate() error {
	return c.Interval.Validate()
}

// Validate ensures that MpsConfig has a valid set of values.
func (c *MpsConfig) Validate() error {
	if c.DefaultActiveThreadPercentage != nil {
		if *c.DefaultActiveThreadPercentage < 0 {
			return fmt.Errorf("active thread percentage must not be negative")
		}
		if *c.DefaultActiveThreadPercentage > 100 {
			return fmt.Errorf("active thread percentage must not be greater than 100")
		}
	}
	return nil
}

// Validate ensures that GpuSharing has a valid set of values.
func (s *GpuSharing) Validate() error {
	if err := s.Strategy.Validate(); err != nil {
		return err
	}
	switch {
	case s.IsTimeSlicing():
		return s.TimeSlicingConfig.Validate()
	case s.IsMps():
		return s.MpsConfig.Validate()
	}
	return fmt.Errorf("invalid GPU sharing settings: %v", s)
}

// Validate ensures that MigDeviceSharing has a valid set of values.
func (s *MigDeviceSharing) Validate() error {
	if err := s.Strategy.Validate(); err != nil {
		return err
	}
	if s.IsTimeSlicing() {
		return nil
	}
	if s.IsMps() {
		return s.MpsConfig.Validate()
	}
	return fmt.Errorf("invalid MIG device sharing settings: %v", s)
}
