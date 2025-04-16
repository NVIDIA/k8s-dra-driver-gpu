#!/bin/bash

# Copyright 2025 The Kubernetes Authors.
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

set -o errexit
set -o nounset
set -o pipefail

SOURCE_DIR="$(cd "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT_DIR="$SOURCE_DIR/.."

HELM_CHART=$ROOT_DIR/deployments/helm/nvidia-dra-driver-gpu
E2E_IMAGE_REPO=${E2E_IMAGE_REPO:-"ghcr.io/nvidia/k8s-dra-driver-gpu"}
E2E_IMAGE_TAG=${E2E_IMAGE_TAG:-"93cd4799-ubi9"}
E2E_IMAGE_PULL_POLICY=${E2E_IMAGE_PULL_POLICY:-"IfNotPresent"}
ENABLE_GFD=${ENABLE_GFD:-"true"}
E2E_HOST_MANAGED_DRIVERS=${E2E_HOST_MANAGED_DRIVERS:-"true"}
LOG_ARTIFACTS_DIR="$ROOT_DIR/e2e_artifacts"

export E2E_IMAGE_REPO HELM_CHART E2E_IMAGE_TAG E2E_IMAGE_PULL_POLICY ENABLE_GFD E2E_HOST_MANAGED_DRIVERS LOG_ARTIFACTS_DIR

# shellcheck disable=SC2086
make test-e2e
