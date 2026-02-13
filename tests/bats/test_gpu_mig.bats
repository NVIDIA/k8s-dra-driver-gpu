# shellcheck disable=SC2148
# shellcheck disable=SC2329

# bats file_tags=gpu,mig

# Executed before entering each test in this file.
setup() {
  load 'helpers.sh'
  _common_setup
  log_objects

  # Try to establish rather precise state right before entering test. Any
  # previous partial cleanup or partial test might leave an exotic resource
  # slice state behind. Delete those. Delete DRA driver before, if it exists.
  helm uninstall -n nvidia-dra-driver-gpu nvidia-dra-driver-gpu-batssuite --wait || true
  mig_ensure_teardown_on_all_nodes
  kubectl delete resourceslices.resource.k8s.io --all

  # For measuring duration with sub-second precision since start of the test.
  # Used in `log()`
  export _T0=$(awk '{print $1}' /proc/uptime)
}


# Run after each test in this file.
teardown() {
  mig_ensure_teardown_on_all_nodes
}


# bats::on_failure will be called before teardown.
bats::on_failure() {
  echo -e "\n\nFAILURE HOOK START"
  log_objects
  show_kubelet_plugin_error_logs
  show_kubelet_plugin_log_tails
  kubectl describe pods | grep -A20 "Events:"
  echo -e "FAILURE HOOK END\n\n"
}


# bats test_tags=XXbats:focus
# bats test_tags=fastfeedback
@test "StaticMIG: allocate (1 cnt)" {
  # Pick a node to work on for the remainder of the test.
  local node=$(kubectl get nodes | grep worker | head -n1 | awk '{print $1}')
  log "selected node: $node"

  # Create MIG device via mig-manager pod, start DRA driver (announce fresh slices)
  mig_create_1g0_on_node "$node"

  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" NOARGS

  # Run workload, validate `nvidia-smi -L` output in container.
  kubectl apply -f tests/bats/specs/gpu-anymig.yaml
  kubectl wait --for=condition=READY pods pod-anymig --timeout=10s
  log "workload READY"

  run kubectl logs pod-anymig
  assert_output --partial "UUID: MIG-"
  assert_output --partial "UUID: GPU-"

  timeout -v 10 kubectl delete -f tests/bats/specs/gpu-anymig.yaml
  kubectl wait --for=delete pods pod-anymig --timeout=10s
}


# bats test_tags=fastfeedback
@test "StaticMIG: inspect device attributes in resource slice (mig)" {
  local node=$(kubectl get nodes | grep worker | head -n1 | awk '{print $1}')
  log "selected node: $node"
  mig_create_1g0_on_node "$node"
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" NOARGS

  local reference=(
    "architecture"
    "brand"
    "cudaComputeCapability"
    "cudaDriverVersion"
    "driverVersion"
    "productName"
    "resource.kubernetes.io/pciBusID"
    "resource.kubernetes.io/pcieRoot"
    "type"
    "uuid"
    "parentUUID"
    "profile"
    "addressingMode"
  )

  local attrs=$(get_device_attrs_from_any_gpu_slice "mig" "$node")
  assert_attrs_equal "$attrs" "${reference[@]}"
}


# bats test_tags=fastfeedback
@test "StaticMIG: mutual exclusivity with physical GPU" {
  # Pick a node to work on for the remainder of the test.
  local node=$(kubectl get nodes | grep worker | head -n1 | awk '{print $1}')
  echo "selected node: $node"

  # (Re)install, also to refresh ResourceSlice objects.
  local _iargs=("--set" "logVerbosity=6")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs

  # Show RS content for debugging. Confirm no MIG device announced.
  local rsname=$(kubectl get resourceslices.resource.k8s.io | grep "$node" | grep gpu | awk '{print $1}')
  echo "rsname: $rsname"
  kubectl get resourceslices.resource.k8s.io -o yaml "$rsname" | grep -e 'GPU-' -e 'MIG-'
  run kubectl get resourceslices.resource.k8s.io -o yaml "$rsname"
  refute_output --partial "MIG-"

  # Extract the total number of devices announced by said resource slice.
  local dev_count_before=$(kubectl get  resourceslices.resource.k8s.io -o yaml "$rsname" | yq '.spec.devices | length')
  echo "devices announced (before): ${dev_count_before}"

  # Be sure to delete that old resource slice: the next time we look at the GPU
  # resource slice on this node we must know it's a fresh one.
  helm uninstall -n nvidia-dra-driver-gpu nvidia-dra-driver-gpu-batssuite --wait
  kubectl delete resourceslices.resource.k8s.io "$rsname"

  mig_create_1g0_on_node "$node"

  # Install DRA driver again, and inspect the newly created resource slice.
  # Confirm that at least one MIG device is announced in that slice.
  local _iargs=("--set" "logVerbosity=6")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs
  local rsname=$(kubectl get resourceslices.resource.k8s.io | grep "$node" | grep gpu | awk '{print $1}')
  kubectl get  resourceslices.resource.k8s.io -o yaml "$rsname" | grep -e 'GPU-' -e 'MIG-'
  run kubectl get resourceslices.resource.k8s.io -o yaml "$rsname"
  assert_output --partial "MIG-"

  # Obtain the number of devices announced in the new resource slice.
  local dev_count_after=$(kubectl get  resourceslices.resource.k8s.io -o yaml "$rsname" | yq '.spec.devices | length')
  echo "devices announced (after): ${dev_count_after}"

  # The following check detects the bug described in
  # https://github.com/NVIDIA/k8s-dra-driver-gpu/issues/719
  if [ $dev_count_before != $dev_count_after ]; then
    echo "the number of announced devices must stay the same"
    return 1
  fi

  # Success: with one MIG device being announced and the overall number of
  # devices being the same as before we now understand that one parent GPU is
  # _not_ announced anymore (we can enhance precision by comparing UUID sets, if
  # ever necessary).
}

