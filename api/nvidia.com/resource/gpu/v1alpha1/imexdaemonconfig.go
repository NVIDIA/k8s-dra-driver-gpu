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
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ImexDaemonConfig holds the set of parameters for configuring an ImexDaemon.
type ImexDaemonConfig struct {
	metav1.TypeMeta `json:",inline"`
	NumNodes        int    `json:"numNodes"`
	DomainID        string `json:"domainID"`
}

// DefaultImexDaemonConfig provides the default ImexDaemon configuration.
func DefaultImexDaemonConfig() *ImexDaemonConfig {
	return &ImexDaemonConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupName + "/" + Version,
			Kind:       ImexDaemonConfigKind,
		},
	}
}

// Normalize updates a ImexDaemonConfig config with implied default values based on other settings.
func (c *ImexDaemonConfig) Normalize() error {
	return nil
}

// Validate ensures that ImexDaemonConfig has a valid set of values.
func (c *ImexDaemonConfig) Validate() error {
	if c.NumNodes <= 0 {
		return fmt.Errorf("numNodes must be greater than or equal to 1")
	}
	if c.DomainID == "" {
		return fmt.Errorf("domainID cannot be empty")
	}
	return nil
}
