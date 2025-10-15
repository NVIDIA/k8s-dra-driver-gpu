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

# Use a name that upon cluster inspection reveals that this
# Helm chart release was installed/managed by this test suite.
export TEST_HELM_RELEASE_NAME="nvidia-dra-driver-gpu-batssuite"


_common_setup() {
  load '/bats-libraries/bats-support/load.bash'
  load '/bats-libraries/bats-assert/load.bash'
  load '/bats-libraries/bats-file/load.bash'
}


# A helper arg for `iupgrade_wait` w/o additional install args.
export NOARGS=()

# Install or upgrade, and wait for pods to be READY.
# 1st arg: helm chart repo
# 2nd arg: helm chart version
# 3rd arg: array with additional args (provide `NOARGS` if none)
iupgrade_wait() {
  # E.g. `nvidia/nvidia-dra-driver-gpu` or
  # `oci://ghcr.io/nvidia/k8s-dra-driver-gpu`
  local REPO="$1"

  # E.g. `25.3.1` or `25.8.0-dev-f2eaddd6-chart`
  local VERSION="$2"

  # Expect array as third argument.
  local -n ADDITIONAL_INSTALL_ARGS=$3

  timeout -v 120 helm upgrade --install "${TEST_HELM_RELEASE_NAME}" \
    "${REPO}" \
    --version="${VERSION}" \
    --wait \
    --timeout=1m5s \
    --create-namespace \
    --namespace nvidia-dra-driver-gpu \
    --set resources.gpus.enabled=false \
    --set nvidiaDriverRoot="${TEST_NVIDIA_DRIVER_ROOT}" "${ADDITIONAL_INSTALL_ARGS[@]}"

  # Valueable output to have in the logs in case things went pearshaped.
  kubectl get pods -n nvidia-dra-driver-gpu

  # Some part of this waiting work is done by helm as of `--wait` with
  # `--timeout`. Note that the below in itself would not be sufficient: in case
  # of an upgrade we need to isolate the _new_ pods and not accidentally observe
  # the currently disappearing pods. Also note that despite the `--wait` above,
  # the kubelet plugins may still be in `PodInitializing` or `Init:0/1` after
  # the Helm command returned. My conclusion is that helm waits for the
  # controller to be READY, but not for the plugin pods to be READY. An old
  # plugin pod may also still be present in `Completed` state. The label
  # selector below may (rarely) pick it up, and of course that one will never
  # transition to READY. Fix that by only waiting for pods that are not in
  # Terminating/Completed state (that do not have a deletionTimestamp). That's
  # not natively supported by `kubectl wait`, hence this must be something of
  # the shape
  # `kubectl get pods ... | xargs -I{} kubectl wait --for=condition=Ready pod/{} `
  sleep 1
  kubectl wait --for=condition=READY pods -A -l nvidia-dra-driver-gpu-component=kubelet-plugin --timeout=15s

  # Again, log current state.
  kubectl get pods -n nvidia-dra-driver-gpu

  # That one should be obvious now, but make that guarantee explicit for
  # consuming tests.
  kubectl wait --for=condition=READY pods -A -l nvidia-dra-driver-gpu-component=controller --timeout=10s
  # maybe: check version on labels (to confirm that we set labels correctly)
}

# Events accumulate over time, so for certainty it's best to use a unique pod
# name. Right now, this inspects an entire line which includes REASON, MESSAGE,
# and OBJECT, so choose the needle (grepped for) precisely enough.
# Example: wait_for_pod_event pod/testpod-ls09x FailedPrepareDynamicResources 60
wait_for_pod_event() {
  # Expect this to have the pod/ prefix
  local POD_NAME="$1"
  local REASON="$2"
  local TIMEOUT="$3"

  local START=$SECONDS
  while true; do
    if kubectl events --for "${POD_NAME}" | grep -q "${REASON}"; then
      echo "Event detected: ${REASON} (for ${POD_NAME})"
      return 0
    fi
    if (( SECONDS - START > TIMEOUT )); then
      echo "Timeout (${TIMEOUT} s) waiting for '${REASON}' in events for ${POD_NAME}"
      return 1
    fi
    sleep 2
  done
}

get_all_cd_daemon_logs_for_cd_name() {
  CD_NAME="$1"
  CD_UID=$(kubectl describe computedomains.resource.nvidia.com "${CD_NAME}" | grep UID | awk '{print $2}')
  CD_LABEL_KV="resource.nvidia.com/computeDomain=${CD_UID}"
  echo "CD daemon logs for CD: $CD_UID"
  kubectl logs \
    -n nvidia-dra-driver-gpu \
    -l "${CD_LABEL_KV}" \
    --tail=-1 \
    --prefix \
    --all-containers
}

show_kubelet_plugin_error_logs() {
  echo -e "\nKUBELET PLUGIN ERROR LOGS START"
  (
    kubectl logs \
    -l nvidia-dra-driver-gpu-component=kubelet-plugin \
    -n nvidia-dra-driver-gpu \
    --prefix --tail=-1 | grep -E "^(E|W)[0-9]{4}"
  ) || true
  echo -e "KUBELET PLUGIN ERROR LOGS END\n\n"
}


# Intended use case: one pod in Running or ContainerCreating state; then this
# function returns the specific name of that pod. Specifically, ignore pods that
# were just deleted or are terminating (this is important during the small time
# window of restarting the controller, say in response to a deployment podspec
# template mutation).
get_current_controller_pod_name() {
  kubectl get pod \
    -l nvidia-dra-driver-gpu-component=controller \
    -n nvidia-dra-driver-gpu \
      | grep -iv "NAME" \
      | grep -iv 'completed' \
      | grep -iv 'terminating' \
      | awk '{print $1}'
}
