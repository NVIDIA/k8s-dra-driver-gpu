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

: ${PROJECT_NAME:=$(gcloud config list --format 'value(core.project)' 2>/dev/null)}

CURRENT_DIR="$(cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd)"
PROJECT_DIR="$(cd -- "$( dirname -- "${CURRENT_DIR}/../../../.." )" &> /dev/null && pwd)"

# We extract information from versions.mk
function from_versions_mk() {
    local makevar=$1
    local value=$(grep -E "^\s*${makevar}\s+[\?:]= " ${PROJECT_DIR}/versions.mk)
    echo ${value##*= }
}
DRIVER_NAME=$(from_versions_mk "DRIVER_NAME")

NETWORK_NAME="${DRIVER_NAME}-net"
CLUSTER_NAME="${DRIVER_NAME}-cluster"
ROUTER_REGION="us-central1"
REGION="us-central1-c"

## Delete the cluster
gcloud container clusters delete "${CLUSTER_NAME}" \
	--quiet \
	--project "${PROJECT_NAME}" \
	--region "${REGION}"

## Delete the nat config
gcloud compute routers nats delete "${NETWORK_NAME}-nat-config" \
	--quiet \
	--project "${PROJECT_NAME}" \
    --router "${NETWORK_NAME}-nat-router" \
    --router-region "${ROUTER_REGION}"

## Delete the nat router
gcloud compute routers delete ${NETWORK_NAME}-nat-router \
	--quiet \
	--project "${PROJECT_NAME}" \
	--region "${ROUTER_REGION}"

## Delete the network
gcloud compute networks delete "${NETWORK_NAME}" \
	--quiet \
	--project "${PROJECT_NAME}"
