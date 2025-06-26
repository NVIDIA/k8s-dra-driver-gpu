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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ComputeDomainChannelConfig holds the set of parameters for configuring an ComputeDomainChannel.
type ComputeDomainChannelConfig struct {
	metav1.TypeMeta `json:",inline"`
	DomainID        string `json:"domainID"`
}

// DefaultComputeDomainChannelConfig provides the default ComputeDomainChannel configuration.
func DefaultComputeDomainChannelConfig() *ComputeDomainChannelConfig {
	return &ComputeDomainChannelConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupName + "/" + Version,
			Kind:       ComputeDomainChannelConfigKind,
		},
	}
}

// Normalize updates a ComputeDomainChannelConfig config with implied default values based on other settings.
func (c *ComputeDomainChannelConfig) Normalize() error {
	return nil
}

// Validate ensures that ComputeDomainDaemonConfig has a valid set of values.
func (c *ComputeDomainChannelConfig) Validate() error {
	if c.DomainID == "" {
		return fmt.Errorf("domainID cannot be empty")
	}
	return nil
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ComputeDomainDaemonConfig holds the set of parameters for configuring an ComputeDomainDaemon.
type ComputeDomainDaemonConfig struct {
	metav1.TypeMeta `json:",inline"`
	DomainID        string `json:"domainID"`
}

// DefaultComputeDomainDaemonConfig provides the default ComputeDomainDaemon configuration.
func DefaultComputeDomainDaemonConfig() *ComputeDomainDaemonConfig {
	return &ComputeDomainDaemonConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupName + "/" + Version,
			Kind:       ComputeDomainDaemonConfigKind,
		},
	}
}

// Normalize updates a ComputeDomainDaemonConfig config with implied default values based on other settings.
func (c *ComputeDomainDaemonConfig) Normalize() error {
	return nil
}

// Validate ensures that ComputeDomainDaemonConfig has a valid set of values.
func (c *ComputeDomainDaemonConfig) Validate() error {
	if c.DomainID == "" {
		return fmt.Errorf("domainID cannot be empty")
	}
	return nil
}
