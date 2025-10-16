#!/bin/bash
#
#  SPDX-FileCopyrightText: Copyright (c) 2025 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
#  SPDX-License-Identifier: Apache-2.0
#
#  Licensed under the Apache License, Version 2.0 (the "License");
#  you may not use this file except in compliance with the License.
#  You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
#  Unless required by applicable law or agreed to in writing, software
#  distributed under the License is distributed on an "AS IS" BASIS,
#  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#  See the License for the specific language governing permissions and
#  limitations under the License.

# Reference:
# https://bats-core.readthedocs.io/en/latest/writing-tests.html#setup-and-teardown-pre-and-post-test-hooks

# Validate that some prerequisites are met, and inspect environment for
# characteristics, such as DRA API group version. A failing setup_suite()
# function aborts the suite (fail fast).
validate_prerequisites() {
    # Probe: kubectl configured against a k8s cluster.
    kubectl cluster-info | grep "control plane is running at"

    # Fail fast in case there seems to be a DRA driver Helm chart installed at
    # this point (maybe one _not_ managed by this test suite).
    helm list -A
    helm list -A | grep "nvidia-dra-driver-gpu" && { echo "error: helm list not clean"; return 1; }

    # Show, for debugging.
    kubectl api-resources --api-group=resource.k8s.io

    # Require DRA API group to be enabled (for now, maybe we want to add a test
    # later that shows how Helm chart installation fails when DRA is not
    # enabled, and then this suite-setup check could get in the way). The
    # command below is expected to emit deviceclasses, resourceclaims,
    # resourceclaimtemplates, resourceslices -- probe just one.
    kubectl api-resources --api-group=resource.k8s.io | grep resourceslices

    TEST_K8S_RESOURCE_API_VERSION=$( \
        kubectl api-resources --api-group=resource.k8s.io -o json | \
        jq -r '.resources | map(select(.version)) | .[0].version'
    )

    # Examples: v1, or v1beta1
    export TEST_K8S_RESOURCE_API_VERSION

    # For resource.k8s.io API versions before v1, a ResourceClaim spec looks
    # slightly differently. Use flow-style YAML.
    export TEST_DEVCLASSNAME_CD_CHANNEL="exactly: {deviceClassName: compute-domain-default-channel.nvidia.com}"
    if [[ "${TEST_K8S_RESOURCE_API_VERSION}" != "v1" ]]; then
        export TEST_DEVCLASSNAME_CD_CHANNEL="deviceClassName: compute-domain-default-channel.nvidia.com"
    fi
}


setup_suite () {
    validate_prerequisites
    # Create Helm repo cache dir and point `helm` to it, otherwise `Error:
    # INSTALLATION FAILED: mkdir /.cache: permission denied`
    HELM_REPOSITORY_CACHE=$(mktemp -d -t helm-XXXXX)
    export HELM_REPOSITORY_CACHE

    # Consumed by the helm CLI.
    export HELM_REPOSITORY_CONFIG=${HELM_REPOSITORY_CACHE}/repo.cfg

    # Prepare CRD upgrade URL.
    export CRD_URL_PFX="https://raw.githubusercontent.com/NVIDIA/k8s-dra-driver-gpu/"
    export CRD_URL_SFX="/deployments/helm/nvidia-dra-driver-gpu/crds/resource.nvidia.com_computedomains.yaml"
    export CRD_UPGRADE_URL="${CRD_URL_PFX}${TEST_CRD_UPGRADE_TARGET_GIT_REF}${CRD_URL_SFX}"

    # Prepare for installing releases from NGC (that merely mutates local
    # filesystem state).
    helm repo add nvidia https://helm.ngc.nvidia.com/nvidia && helm repo update
}


