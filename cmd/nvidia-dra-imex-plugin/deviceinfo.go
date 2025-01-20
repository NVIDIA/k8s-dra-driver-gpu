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

package main

import (
	"fmt"

	resourceapi "k8s.io/api/resource/v1beta1"
	"k8s.io/utils/ptr"
)

type ImexChannelInfo struct {
	Channel int `json:"channel"`
}

func (d *ImexChannelInfo) CanonicalName() string {
	return fmt.Sprintf("imex-channel-%d", d.Channel)
}

func (d *ImexChannelInfo) CanonicalIndex() string {
	return fmt.Sprintf("%d", d.Channel)
}

func (d *ImexChannelInfo) GetDevice() resourceapi.Device {
	device := resourceapi.Device{
		Name: d.CanonicalName(),
		Basic: &resourceapi.BasicDevice{
			Attributes: map[resourceapi.QualifiedName]resourceapi.DeviceAttribute{
				"type": {
					StringValue: ptr.To(ImexChannelType),
				},
				//"domain": {
				//	StringValue: ptr.To(ImexChannelType),
				//},
				"channel": {
					IntValue: ptr.To(int64(d.Channel)),
				},
			},
		},
	}
	return device
}
