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
)

const (
	ComputeDomainStatusReady    = "Ready"
	ComputeDomainStatusNotReady = "NotReady"

	ComputeDomainChannelAllocationModeSingle = "Single"
	ComputeDomainChannelAllocationModeAll    = "All"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +k8s:openapi-gen=true
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:subresource:status

// ComputeDomain prepares a set of nodes to run a multi-node workload in.
type ComputeDomain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ComputeDomainSpec   `json:"spec,omitempty"`
	Status ComputeDomainStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ComputeDomainList provides a list of ComputeDomains.
type ComputeDomainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []ComputeDomain `json:"items"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="A computeDomain.spec is immutable"

// ComputeDomainSpec provides the spec for a ComputeDomain.
type ComputeDomainSpec struct {
	NumNodes int                       `json:"numNodes"`
	Channel  *ComputeDomainChannelSpec `json:"channel"`
}

// ComputeDomainChannelSpec provides the spec for a channel used to run a workload inside a ComputeDomain.
type ComputeDomainChannelSpec struct {
	ResourceClaimTemplate ComputeDomainResourceClaimTemplate `json:"resourceClaimTemplate"`
	// Allows for requesting all IMEX channels (the maximum per IMEX domain) or
	// precisely one.
	// +kubebuilder:validation:Enum=All;Single
	// +kubebuilder:default=Single
	AllocationMode string `json:"allocationMode"`
}

// ComputeDomainResourceClaimTemplate provides the details of the ResourceClaimTemplate to generate.
type ComputeDomainResourceClaimTemplate struct {
	Name string `json:"name"`
}

// ComputeDomainStatus provides the status for a ComputeDomain.
type ComputeDomainStatus struct {
	// +kubebuilder:validation:Enum=Ready;NotReady
	// +kubebuilder:default=NotReady
	Status string `json:"status"`
	// +listType=map
	// +listMapKey=name
	Nodes []*ComputeDomainNode `json:"nodes,omitempty"`
}

// ComputeDomainNode provides information about each node added to a ComputeDomain.
type ComputeDomainNode struct {
	Name      string `json:"name"`
	IPAddress string `json:"ipAddress"`
	CliqueID  string `json:"cliqueID"`
	Index     int    `json:"index"`
}
