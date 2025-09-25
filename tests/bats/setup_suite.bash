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
# characteristics, such as DRA API group version. A failing setup_suit()
# function aborts the suite (fail fast).
setup_suite () {
    # Probe: kubectl configured against a k8s cluster.
    kubectl cluster-info | grep "control plane is running at"

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
}