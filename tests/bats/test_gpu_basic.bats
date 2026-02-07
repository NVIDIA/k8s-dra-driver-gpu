# shellcheck disable=SC2148
# shellcheck disable=SC2329

setup_file () {
  load 'helpers.sh'
  _common_setup
  local _iargs=("--set" "logVerbosity=6")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs
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
  echo -e "FAILURE HOOK END\n\n"
}


# bats test_tags=fastfeedback
@test "GPUs: 1 pod(s), 1 full GPU" {
  local _specpath="tests/bats/specs/gpu-simple-full.yaml"
  local _podname="pod-full-gpu"

  kubectl apply -f "${_specpath}"
  kubectl wait --for=condition=READY pods "${_podname}" --timeout=8s

  # Confirm the following pattern:
  # GPU 0: NVIDIA GB200 (UUID: GPU-7277883e-ce1e-3b6e-6cc1-6d52e80cdb86)
  run kubectl logs "${_podname}"
  assert_output --partial "UUID: GPU-"
  echo "${output}" | wc -l | grep 1

  kubectl delete -f  "${_specpath}"
  kubectl wait --for=delete pods "${_podname}" --timeout=10s
}


# bats test_tags=fastfeedback
@test "GPUs: 2 pod(s), 1 full GPU each" {
  local _specpath="tests/bats/specs/gpu-2pods-2gpus.yaml"

  kubectl apply -f "${_specpath}"
  kubectl wait --for=condition=READY pods pod1 --timeout=8s
  kubectl wait --for=condition=READY pods pod2 --timeout=8s

  run kubectl logs pod1
  assert_output --partial "UUID: GPU-"
  echo "${output}" | wc -l | grep 1
  local uid1="${output}"

  run kubectl logs pod2
  assert_output --partial "UUID: GPU-"
  echo "${output}" | wc -l | grep 1
  local uid2="${output}"

  assert_not_equal "$uid1" "$uid2"

  kubectl delete -f  "${_specpath}"
  kubectl wait --for=delete pods pod1 --timeout=10s
  kubectl wait --for=delete pods pod2 --timeout=10s
}


# bats test_tags=fastfeedback
@test "GPUs: 2 pod(s), 1 full GPU (shared, 1 RC)" {
  local _specpath="tests/bats/specs/gpu-2pods-1gpu.yaml"

  kubectl apply -f "${_specpath}"
  kubectl wait --for=condition=READY pods pod1 --timeout=8s
  kubectl wait --for=condition=READY pods pod2 --timeout=8s

  run kubectl logs pod1
  assert_output --partial "UUID: GPU-"
  echo "${output}" | wc -l | grep 1
  local uid1="${output}"

  run kubectl logs pod2
  assert_output --partial "UUID: GPU-"
  echo "${output}" | wc -l | grep 1
  local uid2="${output}"

  assert_equal "$uid1" "$uid2"

  kubectl delete -f  "${_specpath}"
  kubectl wait --for=delete pods pod1 --timeout=10s
  kubectl wait --for=delete pods pod2 --timeout=10s
}


# bats test_tags=fastfeedback
@test "GPUs: 1 pod(s), 2 cntrs, 1 full GPU (shared, 1 RCT)" {
  local _specpath="tests/bats/specs/gpu-1pod-2cnt-1gpu.yaml"

  kubectl apply -f "${_specpath}"
  kubectl wait --for=condition=READY pods pod1 --timeout=8s

  run kubectl logs pod1 -c ctr0
  assert_output --partial "UUID: GPU-"
  echo "${output}" | wc -l | grep 1
  local uid1="${output}"

  run kubectl logs pod1 -c ctr1
  assert_output --partial "UUID: GPU-"
  echo "${output}" | wc -l | grep 1
  local uid2="${output}"

  assert_equal "$uid1" "$uid2"

  kubectl delete -f  "${_specpath}"
  kubectl wait --for=delete pods pod1 --timeout=10s
}
