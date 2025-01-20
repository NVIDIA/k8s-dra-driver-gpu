/**
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
**/

package main

import (
	"os"
	"path/filepath"
)

type root string

// isDevRoot checks whether the specified root is a dev root.
// A dev root is defined as a root containing a /dev folder.
func (r root) isDevRoot() bool {
	stat, err := os.Stat(filepath.Join(string(r), "dev"))
	if err != nil {
		return false
	}
	return stat.IsDir()
}

// getDevRoot returns the dev root associated with the root.
// If the root is not a dev root, this defaults to "/".
func (r root) getDevRoot() string {
	if r.isDevRoot() {
		return string(r)
	}
	return "/"
}
