#!/usr/bin/env bash
# Copyright 2025 NVIDIA CORPORATION
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

# Currently, this is hard-coded to the sha corresponding to the 1.18.0-rc.1 release
# of the nvidia-container-toolkit. In the future, we should determine the exact
# commit SHA corresponding to the version of the go module for the dependency
# on `github.com/NVIDIA/nvidia-container-toolkit` -— whether it is a
# tagged release or a pseudo-version -- and return that SHA instead.
TOOLKIT_VERSION_SHA="4f98c014bfa1222a0b1dda34ec815f5ecf87c971"

echo ghcr.io/nvidia/container-toolkit:${TOOLKIT_VERSION_SHA:0:8}
