/*
# Copyright 2023 NVIDIA CORPORATION
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
*/

package main

// cdiOption represents a functional option for constructing a CDI handler.
type cdiOption func(*CDIHandler)

// WithDriverRoot provides an cdiOption to set the driver root used by the 'cdi' interface.
func WithDriverRoot(root string) cdiOption {
	return func(c *CDIHandler) {
		c.driverRoot = root
	}
}

// WithDevRoot provides a cdiOption to set the device root used by the 'cdi' interface.
func WithDevRoot(root string) cdiOption {
	return func(c *CDIHandler) {
		c.devRoot = root
	}
}

// WithTargetDriverRoot provides an cdiOption to set the target driver root used by the 'cdi' interface.
func WithTargetDriverRoot(root string) cdiOption {
	return func(c *CDIHandler) {
		c.targetDriverRoot = root
	}
}

// WithCDIRoot provides an cdiOption to set the CDI root used by the 'cdi' interface.
func WithCDIRoot(cdiRoot string) cdiOption {
	return func(c *CDIHandler) {
		c.cdiRoot = cdiRoot
	}
}

// WithVendor provides an cdiOption to set the vendor used by the 'cdi' interface.
func WithVendor(vendor string) cdiOption {
	return func(c *CDIHandler) {
		c.vendor = vendor
	}
}
