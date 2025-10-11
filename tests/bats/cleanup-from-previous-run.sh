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


set -o nounset
set -o pipefail

CRD_URL="https://raw.githubusercontent.com/NVIDIA/k8s-dra-driver-gpu/main/deployments/helm/nvidia-dra-driver-gpu/crds/resource.nvidia.com_computedomains.yaml"


THIS_DIR_PATH=$(dirname "$(realpath $0)")
source "${THIS_DIR_PATH}/helpers.sh"

# For debugging: state of the world
kubectl get computedomains.resource.nvidia.com
kubectl get pods -n nvidia-dra-driver-gpu
helm list -A

set -x

# If a previous run leaves e.g. the controller behind in CrashLoopBackOff then
# the next installation with --wait won't succeed.
timeout -v 5 helm uninstall nvidia-dra-driver-gpu-batssuite -n nvidia-dra-driver-gpu

# When the CRD has been left behind deleted by a partially performed
# test then the deletions below cannot succeed. Apply a CRD version that
# likely helps deletion.
kubectl apply -f "${CRD_URL}"

# Workload deletion below requires a DRA driver to be present, to actually clean
# up. Install _a_ version temporarily, towards best-effort. Install
# to-be-tested-version for now, latest-on-GHCR might be smarter though. Again,
# this command may fail and in best-effort fashion this cleanup script proceeds.
iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" NOARGS

# Some effort to delete workloads potentially left-over from a previous
# interrupted run. TODO: try to affect all-at-once, maybe with a special label.
# Note: the following commands are OK to fail -- the `errexit` shell option is
# deliberately not set here.
timeout -v 5 kubectl delete -f demo/specs/imex/channel-injection.yaml 2> /dev/null
timeout -v 5 kubectl delete -f demo/specs/imex/channel-injection-all.yaml 2> /dev/null
timeout -v 5 kubectl delete jobs nickelpie-test 2> /dev/null
timeout -v 5 kubectl delete computedomain nickelpie-test-compute-domain 2> /dev/null
timeout -v 5 kubectl delete -f demo/specs/imex/nvbandwidth-test-job-1.yaml 2> /dev/null
timeout -v 5 kubectl delete pods -l env=batssuite 2> /dev/null
timeout -v 2 kubectl delete resourceclaim batssuite-rc-bad-opaque-config --force 2> /dev/null

# TODO: maybe more brute-forcing/best-effort: it might make sense to submit all
# workload in this test suite into a special namespace (not `default`), and to
# then use `kubectl delete pods -n <testnamespace]> --all`.

# Delete any previous remainder of `clean-state-dirs-all-nodes.sh` invocation.
kubectl delete pods privpod-rm-plugindirs 2> /dev/null

timeout -v 5 helm uninstall nvidia-dra-driver-gpu-batssuite -n nvidia-dra-driver-gpu

kubectl wait \
    --for=delete pods -A \
    -l app.kubernetes.io/name=nvidia-dra-driver-gpu \
    --timeout=10s \
        || echo "wait-for-delete failed"

# The next `helm install` should freshly install CRDs, and this is one way to
# try to achieve that. This might time out in case workload wasn't cleaned up
# properly. If that happens, the next test suite invocation will have failures
# like "create not allowed while custom resource definition is terminating".
timeout -v 10 kubectl delete crds computedomains.resource.nvidia.com || echo "CRD deletion failed"

# Remove kubelet plugin state directories from all nodes (critical part of
# cleanup, fail hard if this does not succeed).
set -e
bash tests/bats/clean-state-dirs-all-nodes.sh
set +x
