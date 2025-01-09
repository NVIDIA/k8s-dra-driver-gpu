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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ImexChannelConfig holds the set of parameters for configuring an ImexChannel.
type ImexChannelConfig struct {
	metav1.TypeMeta `json:",inline"`
}

// DefaultImexChannelConfig provides the default ImexChannel configuration.
func DefaultImexChannelConfig() *ImexChannelConfig {
	return &ImexChannelConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupName + "/" + Version,
			Kind:       ImexChannelConfigKind,
		},
	}
}

// Normalize updates a ImexChannelConfig config with implied default values based on other settings.
func (c *ImexChannelConfig) Normalize() error {
	return nil
}

// Validate ensures that ImexChannelConfig has a valid set of values.
func (c *ImexChannelConfig) Validate() error {
	return nil
}
