#!/bin/bash

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
PROJECT_DIR="$(cd -- "$( dirname -- "${CURRENT_DIR}/../../../.." )" &> /dev/null && pwd)"

# We extract information from versions.mk
function from_versions_mk() {
    local makevar=$1
    local value=$(grep -E "^\s*${makevar}\s+[\?:]= " ${PROJECT_DIR}/versions.mk)
    echo ${value##*= }
}
DRIVER_NAME=$(from_versions_mk "DRIVER_NAME")

: ${IMAGE_REGISTRY:=ghcr.io/nvidia}
: ${IMAGE_NAME:=${DRIVER_NAME}}
: ${IMAGE_TAG:=6c34f5fb-ubi8}

helm upgrade -i --create-namespace --namespace nvidia nvidia-dra-driver ${PROJECT_DIR}/deployments/helm/k8s-dra-driver \
  --set image.repository=${IMAGE_REGISTRY}/${IMAGE_NAME} \
  --set image.tag=${IMAGE_TAG} \
  --set image.pullPolicy=Always \
  --set controller.priorityClassName="" \
  --set kubeletPlugin.priorityClassName="" \
  --set deviceClasses="{gpu,mig}" \
  --set nvidiaDriverRoot="/opt/nvidia" \
  --set kubeletPlugin.tolerations[0].key=nvidia.com/gpu \
  --set kubeletPlugin.tolerations[0].operator=Exists \
  --set kubeletPlugin.tolerations[0].effect=NoSchedule \
  --set kubeletPlugin.tolerations[1].key=cloud.google.com/compute-class \
  --set kubeletPlugin.tolerations[1].operator=Exists \
  --set kubeletPlugin.tolerations[1].effect=NoSchedule
