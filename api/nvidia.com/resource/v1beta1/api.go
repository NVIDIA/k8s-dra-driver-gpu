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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
)

const (
	GroupName = "resource.nvidia.com"
	Version   = "v1beta1"

	GpuConfigKind                  = "GpuConfig"
	MigDeviceConfigKind            = "MigDeviceConfig"
	ComputeDomainChannelConfigKind = "ComputeDomainChannelConfig"
	ComputeDomainDaemonConfigKind  = "ComputeDomainDaemonConfig"
	ComputeDomainKind              = "ComputeDomain"
)

// Interface defines the set of common APIs for all configs
// +k8s:deepcopy-gen=false
type Interface interface {
	Normalize() error
	Validate() error
}

// Decoder implements a decoder for objects in this API group.
var Decoder runtime.Decoder

func init() {
	// Create a new scheme and add our types to it. If at some point in the
	// future a new version of the configuration API becomes necessary, then
	// conversion functions can be generated and registered to continue
	// supporting older versions.
	scheme := runtime.NewScheme()
	schemeGroupVersion := schema.GroupVersion{
		Group:   GroupName,
		Version: Version,
	}
	scheme.AddKnownTypes(schemeGroupVersion,
		&GpuConfig{},
		&MigDeviceConfig{},
		&ComputeDomainChannelConfig{},
		&ComputeDomainDaemonConfig{},
		&ComputeDomain{},
	)
	metav1.AddToGroupVersion(scheme, schemeGroupVersion)

	// Set up a json serializer to decode our types.
	Decoder = json.NewSerializerWithOptions(
		json.DefaultMetaFactory,
		scheme,
		scheme,
		json.SerializerOptions{
			// Note: the strictness applies to all types defined above via
			// AddKnownTypes(), i.e. it cannot be set per-type. That is OK in
			// this case. Unknown fields will simply be dropped (ignored) upon
			// decode, which is what we want. This is relevant in a downgrade
			// case, when a checkpointed JSON document contains fields added in
			// a later version (workload defined with a new version of this
			// driver).
			Pretty: true, Strict: false,
		},
	)
}
