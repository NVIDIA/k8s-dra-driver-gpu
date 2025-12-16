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

# Parse go.mod entry for `github.com/NVIDIA/nvidia-container-toolkit`. Construct
# and output ctk container image locator corresponding to that.

# The image locator is meant for consumption at DRA driver container image build
# time (passed into the build as build argument). Currently used to read the
# `nvidia-cdi-hook` binary from the toolkits container image.
#
# Distinguish two cases:
#
# - Format 'vX.Y.Z-time-commit': use ghcr.io with commit SHA (dev builds)
# - Format 'vX.Y.Z'            : use nvcr.io (releases)
#
# Assume consumption via `$(shell ...)` which ignores the exit code. Indicate
# result on stdout:
#
#  - Success: emit URL on stdout
#  - Failure: emit TOOLKIT_VERSION_NOT_SET or TOOLKIT_VERSION_PARSE_FAILED.
#
# Currently executed upon including `versions.mk`, i.e. for all targets. Hence,
# do not emit errors on `stderr`, but gracefully degrade (many Makefile targets
# powered by versions.mk still work correctly with TOOLKIT_VERSION_NOT_SET).

set -euo pipefail

# Find first line containing `github.com/NVIDIA/nvidia-container-toolkit` and
# then extract second token.
TOOLKIT_VERSION=$(cat go.mod | grep -m 1 'github.com/NVIDIA/nvidia-container-toolkit' | awk '{print $2}')

if [ -z "${TOOLKIT_VERSION}" ]; then
    echo "TOOLKIT_VERSION_NOT_SET"
    exit 0
fi

# Handle format vX.Y.Z-time-commit
if [[ "${TOOLKIT_VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+-[0-9]{14}-([a-f0-9]{12})$ ]]; then
    TOOLKIT_VERSION_SHA="${BASH_REMATCH[1]}"
    SHORT_SHA="${TOOLKIT_VERSION_SHA:0:8}"
    IMAGE_URL="ghcr.io/nvidia/container-toolkit:${SHORT_SHA}"
# Handle format vX.Y.Z, drop leading 'v'
elif [[ "${TOOLKIT_VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
    IMAGE_TAG="${TOOLKIT_VERSION#v}"
    IMAGE_URL="nvcr.io/nvidia/k8s/container-toolkit:v${IMAGE_TAG}"
else
    echo "Error: Unexpected version format: ${TOOLKIT_VERSION}" >&2
    echo "TOOLKIT_VERSION_PARSE_FAILED"
    exit 0
fi

echo "${IMAGE_URL}"
