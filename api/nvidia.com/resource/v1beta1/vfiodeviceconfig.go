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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/NVIDIA/k8s-dra-driver-gpu/pkg/featuregates"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VfioDeviceConfig holds the set of parameters for configuring a VFIO device.
type VfioDeviceConfig struct {
	metav1.TypeMeta `json:",inline"`
}

// DefaultVfioDeviceConfig provides the default configuration of a VFIO device.
func DefaultVfioDeviceConfig() *VfioDeviceConfig {
	if !featuregates.Enabled(featuregates.PassthroughSupport) {
		return nil
	}
	return &VfioDeviceConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupName + "/" + Version,
			Kind:       VfioDeviceConfigKind,
		},
	}
}

// Normalize updates a VfioDeviceConfig config with implied default values based on other settings.
func (c *VfioDeviceConfig) Normalize() error {
	return nil
}

// Validate ensures that VfioDeviceConfig has a valid set of values.
func (c *VfioDeviceConfig) Validate() error {
	return nil
}
