# shellcheck disable=SC2148
# shellcheck disable=SC2329

setup_file () {
  load 'helpers.sh'
  _common_setup
  local _iargs=("--set" "logVerbosity=6" "--set" "featureGates.DynamicMIG=true")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs
  run kubectl logs \
    -l nvidia-dra-driver-gpu-component=kubelet-plugin \
    -n nvidia-dra-driver-gpu \
    -c gpus \
    --prefix --tail=-1
  assert_output --partial "About to announce device gpu-0-mig-1g"
}

# Executed before entering each test in this file.
setup() {
  load 'helpers.sh'
  _common_setup
  log_objects
}


bats::on_failure() {
  echo -e "\n\nFAILURE HOOK START"
  log_objects
  show_kubelet_plugin_error_logs
  show_gpu_plugin_log_tails
  kubectl describe pods | grep -A20 "Events:"
  echo -e "FAILURE HOOK END\n\n"
}


confirm_mig_mode_disabled_all_nodes() {
  # Confirm that MIG mode is disabled for all GPUs on all nodes.
  run nvmm all sh -c 'nvidia-smi --query-gpu=index,mig.mode.current --format=csv'
  refute_output --partial "Enabled"
}


# bats test_tags=fastfeedback
@test "DynMIG: 1 pod, 1 MIG" {
  confirm_mig_mode_disabled_all_nodes
  kubectl apply -f tests/bats/specs/gpu-simple-mig.yaml
  kubectl wait --for=condition=READY pods pod-mig1g --timeout=10s
  run kubectl logs pod-mig1g

  # Confirm the following pattern:
  # GPU 0: NVIDIA GB200 (UUID: GPU-7277883e-ce1e-3b6e-6cc1-6d52e80cdb86)
  #   MIG 1g.24gb     Device  0: (UUID: MIG-5b696ac1-c323-589e-a082-e6045e980bf4)
  assert_output --partial "UUID: MIG-"
  assert_output --partial "UUID: GPU-"

  # Make sure the output contains two lines (first wc -l for debuggability)
  echo "${output}" | wc -l
  echo "${output}" | wc -l | grep 2

  kubectl delete -f tests/bats/specs/gpu-simple-mig.yaml
  kubectl wait --for=delete pods pod-mig1g --timeout=10s
  confirm_mig_mode_disabled_all_nodes
}


# bats test_tags=fastfeedback
@test "DynMIG: 1 pod, 2 containers (1 MIG each)" {
  confirm_mig_mode_disabled_all_nodes

  local _specpath="tests/bats/specs/gpu-multiple-mig.yaml"
  local _podname="pod-2mig"

  kubectl apply -f "${_specpath}"
  kubectl wait --for=condition=READY pods "${_podname}" --timeout=10s

  run kubectl logs "${_podname}" -c ctr0
  assert_output --partial "UUID: MIG-"
  assert_output --partial "UUID: GPU-"
  echo "${output}"
  echo "${output}" | wc -l | grep 2
  mig1uuid="$(echo "${output}" | grep -oP 'MIG-\K[0-9a-f-]+')"

  run kubectl logs "${_podname}" -c ctr1
  assert_output --partial "UUID: MIG-"
  assert_output --partial "UUID: GPU-"
  echo "${output}"
  echo "${output}" | wc -l | grep 2
  mig2uuid="$(echo "${output}" | grep -oP 'MIG-\K[0-9a-f-]+')"

  assert_not_equal "$mig1uuid" "$mig2uuid"

  kubectl delete -f  "${_specpath}"
  kubectl wait --for=delete pods "${_podname}" --timeout=10s
  confirm_mig_mode_disabled_all_nodes
}


# bats test_tags=fastfeedback
@test "DynMIG: 1 pod, 1 MIG + TimeSlicing config" {
  local _iargs=("--set" "logVerbosity=6" "--set" "featureGates.DynamicMIG=true" "--set" "featureGates.TimeSlicingSettings=true")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs

  confirm_mig_mode_disabled_all_nodes
  kubectl apply -f tests/bats/specs/gpu-simple-mig-ts.yaml
  kubectl wait --for=condition=READY pods pod-mig1g --timeout=10s
  run kubectl logs pod-mig1g

  # Confirm the following pattern:
  # GPU 0: NVIDIA GB200 (UUID: GPU-7277883e-ce1e-3b6e-6cc1-6d52e80cdb86)
  #   MIG 1g.24gb     Device  0: (UUID: MIG-5b696ac1-c323-589e-a082-e6045e980bf4)
  assert_output --partial "UUID: MIG-"
  assert_output --partial "UUID: GPU-"

  # Make sure the output contains two lines (first wc -l for debuggability)
  echo "${output}" | wc -l
  echo "${output}" | wc -l | grep 2

  kubectl delete -f tests/bats/specs/gpu-simple-mig-ts.yaml
  kubectl wait --for=delete pods pod-mig1g --timeout=10s
  confirm_mig_mode_disabled_all_nodes
}
