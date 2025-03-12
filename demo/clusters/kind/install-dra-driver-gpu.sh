#!/usr/bin/env bash

# Copyright 2023 The Kubernetes Authors.
# Copyright 2023 NVIDIA CORPORATION.
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

CURRENT_DIR="$(cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd)"

set -ex
set -o pipefail

source "${CURRENT_DIR}/scripts/common.sh"

kubectl label node -l node-role.x-k8s.io/worker --overwrite nvidia.com/gpu.present=true

helm upgrade -i --create-namespace --namespace nvidia-dra-driver-gpu nvidia-dra-driver-gpu ${PROJECT_DIR}/deployments/helm/nvidia-dra-driver-gpu \
    ${NVIDIA_CTK_PATH:+--set nvidiaCtkPath=${NVIDIA_CTK_PATH}} \
    ${NVIDIA_DRIVER_ROOT:+--set nvidiaDriverRoot=${NVIDIA_DRIVER_ROOT}} \
    ${MASK_NVIDIA_DRIVER_PARAMS:+--set maskNvidiaDriverParams=${MASK_NVIDIA_DRIVER_PARAMS}} \
    --set gpuResourcesEnabledOverride=true \
    --wait

set +x
printf '\033[0;32m'
echo "Driver installation complete:"
kubectl get pod -n nvidia-dra-driver-gpu
printf '\033[0m'
