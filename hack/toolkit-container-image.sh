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

# Determines nvidia-container-toolkit image URL based on go.mod version.
# - Pseudo-versions: uses ghcr.io with commit SHA (dev builds)
# - Tagged releases: uses nvcr.io with version tag (stable releases)
# Output is used to pull nvidia-cdi-hook binary from the container.

set -euo pipefail

# Get the resolved version from go.mod using go tooling
TOOLKIT_VERSION=$(go list -m -f '{{.Version}}' github.com/NVIDIA/nvidia-container-toolkit 2>/dev/null || true)

if [ -z "${TOOLKIT_VERSION}" ]; then
    echo "Error: Could not find nvidia-container-toolkit dependency" >&2
    exit 1
fi


if [[ "${TOOLKIT_VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+-[0-9]{14}-([a-f0-9]{12})$ ]]; then
    TOOLKIT_VERSION_SHA="${BASH_REMATCH[1]}"
    SHORT_SHA="${TOOLKIT_VERSION_SHA:0:8}"
    IMAGE_URL="ghcr.io/nvidia/container-toolkit:${SHORT_SHA}"
    
# Check if this is a release-version (format: vx.y.z)
# Remove the 'v' prefix for the image tag (nvcr.io uses x.y.z format)
elif [[ "${TOOLKIT_VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
    IMAGE_TAG="${TOOLKIT_VERSION#v}"
    IMAGE_URL="nvcr.io/nvidia/k8s/container-toolkit:v${IMAGE_TAG}"
else
    echo "Error: Unexpected version format: ${TOOLKIT_VERSION}" >&2
    exit 1
fi

echo "${IMAGE_URL}"
