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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// MigDeviceConfig holds the set of parameters for configuring a MIG device.
type MigDeviceConfig struct {
	metav1.TypeMeta `json:",inline"`
	Sharing         *MigDeviceSharing `json:"sharing,omitempty"`
}

// DefaultMigDeviceConfig provides the default Mig Device configuration.
func DefaultMigDeviceConfig() *MigDeviceConfig {
	config := &MigDeviceConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupName + "/" + Version,
			Kind:       MigDeviceConfigKind,
		},
	}

	if featuregates.Enabled(featuregates.TimeSlicingSettings) {
		config.Sharing = &MigDeviceSharing{
			Strategy: TimeSlicingStrategy,
		}
	}

	return config
}

// Normalize updates a MigDeviceConfig config with implied default values based on other settings.
func (c *MigDeviceConfig) Normalize() error {
	if c.Sharing == nil {
		if !featuregates.Enabled(featuregates.TimeSlicingSettings) {
			return nil
		}
		c.Sharing = &MigDeviceSharing{
			Strategy: TimeSlicingStrategy,
		}
	}

	if featuregates.Enabled(featuregates.MPSSupport) {
		if c.Sharing.Strategy == MpsStrategy && c.Sharing.MpsConfig == nil {
			c.Sharing.MpsConfig = &MpsConfig{}
		}
	}

	return nil
}

// Validate ensures that MigDeviceConfig has a valid set of values.
func (c *MigDeviceConfig) Validate() error {
	if c.Sharing == nil {
		return nil
	}
	return c.Sharing.Validate()
}
