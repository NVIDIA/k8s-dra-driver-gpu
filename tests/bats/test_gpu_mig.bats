# shellcheck disable=SC2148
# shellcheck disable=SC2329

setup_file () {
  load 'helpers.sh'
  _common_setup
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
  kubectl describe pods | grep -A20 "Events:"
  echo -e "FAILURE HOOK END\n\n"
}


# bats test_tags=fastfeedback
@test "static MIG: mutual exclusivity with physical GPU" {
  mig_ensure_teardown_on_all_nodes

  # (Re)install, also to refresh ResourceSlice objects.
  local _iargs=("--set" "logVerbosity=6")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs

  # Pick a node to work on for the remainder of the test.
  local node=$(kubectl get nodes | grep worker | head -n1 | awk '{print $1}')
  echo "selected: $node"

  # Try to clean up after us even in case of failure
  bats::on_failure() {
    nvmm "$node" sh -c 'nvidia-smi mig -dci && nvidia-smi mig -dgi'
    nvmm "$node" nvidia-smi -i 0 -mig 0
  }

  # Get name of the resource slice corresponding to the node selected above.
  local rsname=$(kubectl get resourceslices.resource.k8s.io | grep "$node" | grep gpu | awk '{print $1}')

  # Show slice content for debugging.
  kubectl get resourceslices.resource.k8s.io -o yaml "$rsname" | grep -e 'GPU-' -e 'MIG-'

  # Confirm no MIG device announced.
  run kubectl get resourceslices.resource.k8s.io -o yaml "$rsname"
  refute_output --partial "MIG-"

  # Extract the total number of devices announced by said resource slice.
  local dev_count_before=$(kubectl get  resourceslices.resource.k8s.io -o yaml "$rsname" | yq '.spec.devices | length')
  echo "devices announced (before): ${dev_count_before}"

  # Be sure to delete that old resource slice: the next time we look at the GPU
  # resource slice on this node we must know it's a fresh one.
  helm uninstall -n nvidia-dra-driver-gpu nvidia-dra-driver-gpu-batssuite --wait
  kubectl delete resourceslices.resource.k8s.io "$rsname"

  # Pick a 1g profile (available on all MIG-capable GPUs). Run twice, first time
  # for debuggability
  nvmm "$node" nvidia-smi mig -lgip -i 0 | grep -m 1 -oE '1g\.[1-9]+gb'
  local mprofile=$(nvmm "$node" nvidia-smi mig -lgip -i 0 | grep -m 1 -oE '1g\.[1-9]+gb')

  # Enable MIG mode on the selected node on GPU 0, and create a MIG device
  nvmm "$node" nvidia-smi -i 0 -mig 1
  nvmm "$node" nvidia-smi mig -cgi "$mprofile" -C

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
  mig_ensure_teardown_on_all_nodes
}

