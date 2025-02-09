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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ComputeDomainModeImmediate = "Immediate"
	ComputeDomainModeDelayed   = "Delayed"

	ComputeDomainStatusReady    = "Ready"
	ComputeDomainStatusNotReady = "NotReady"
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
// +kubebuilder:validation:XValidation:rule="self.mode == 'Immediate' || (self.mode == 'Delayed' && size(self.resourceClaimTemplates) == 1)",message="When 'mode' is 'Delayed', 'resourceClaimTemplates' must have exactly one entry."
// +kubebuilder:validation:XValidation:rule="self.mode == 'Immediate' || (self.mode == 'Delayed' && !has(self.nodeSelector))",message="When 'mode' is 'Delayed', 'NodeSelector' must not be set."
// +kubebuilder:validation:XValidation:rule="self.mode == 'Immediate' || (self.mode == 'Delayed' && !has(self.topologyAlignment))",message="When 'mode' is 'Delayed', 'TopologyAlignment' must not be set."
// +kubebuilder:validation:XValidation:rule="self.mode == 'Immediate' || (self.mode == 'Delayed' && (!has(self.nodeAffinity) || !has(self.nodeAffinity.preferred)))",message="When mode is 'Delayed', 'nodeAffinity.preferred' must not be set; only 'nodeAffinity.required' is allowed."
// +kubebuilder:validation:XValidation:rule="self.mode == 'Immediate' || (self.mode == 'Delayed' && !has(self.topologyAntiAlignment))",message="When 'mode' is 'Delayed', 'TopologyAntiAlignment' must not be set."

// ComputeDomainSpec provides the spec for a ComputeDomain.
type ComputeDomainSpec struct {
	// +kubebuilder:validation:Enum=Immediate;Delayed
	// +kubebuilder:default=Immediate
	Mode                   string                               `json:"mode"`
	NumNodes               int                                  `json:"numNodes"`
	ResourceClaimTemplates []ComputeDomainResourceClaimTemplate `json:"resourceClaimTemplates"`
	NodeSelector           map[string]string                    `json:"nodeSelector,omitempty"`
	NodeAffinity           *ComputeDomainNodeAffinity           `json:"nodeAffinity,omitempty"`
	TopologyAlignment      *ComputeDomainTopologyAlignment      `json:"topologyAlignment,omitempty"`
	TopologyAntiAlignment  *ComputeDomainTopologyAlignment      `json:"topologyAntiAlignment,omitempty"`
}

type ComputeDomainResourceClaimTemplate struct {
	Name string `json:"name"`
}

// +kubebuilder:validation:XValidation:rule="has(self.preferred) || has(self.required)",message="At least one of 'preferred' or 'required' must be set."

type ComputeDomainNodeAffinity struct {
	// +listType=atomic
	Preferred []corev1.PreferredSchedulingTerm `json:"preferred,omitempty"`
	Required  *corev1.NodeSelector             `json:"required,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.preferred) || has(self.required)",message="At least one of 'preferred' or 'required' must be set."

type ComputeDomainTopologyAlignment struct {
	// +listType=atomic
	Preferred []ComputeDomainWeightedTopologyKey `json:"preferred,omitempty"`
	Required  *ComputeDomainTopologyKeys         `json:"required,omitempty"`
}

type ComputeDomainTopologyKeys struct {
	// +listType=atomic
	TopologyKeys []string `json:"topologyKeys"`
}

type ComputeDomainWeightedTopologyKey struct {
	Weight      int32  `json:"weight"`
	TopologyKey string `json:"topologyKey"`
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
}
