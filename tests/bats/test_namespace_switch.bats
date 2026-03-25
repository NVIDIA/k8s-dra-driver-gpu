# shellcheck disable=SC2148
# shellcheck disable=SC2329

# Tests for driver installation namespace switching scenarios.
#
# The goal is to ensure that the driver can be reinstalled in a different
# namespace while still allowing management of previously deployed DaemonSets
# (IMEX, MPS), and that resources from the previous namespace are properly
# cleaned up during unprepare.

setup_file() {
  load 'helpers.sh'
  local _iargs=("--set" "logVerbosity=6")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs
}


setup() {
  load 'helpers.sh'
  _common_setup
  log_objects
}


bats::on_failure() {
  echo -e "\n\nFAILURE HOOK START"
  log_objects
  show_kubelet_plugin_error_logs
  echo -e "FAILURE HOOK END\n\n"
}


# bats test_tags=fastfeedback
@test "namespace-switch: IMEX prepare/unprepare and ComputeDomainClique sync across namespaces" {
  # This test verifies that when the driver is switched from one namespace to
  # another (with ADDITIONAL_NAMESPACES pointing to the old namespace):
  # 1. The IMEX daemon controller in the old namespace can create and update
  #    ComputeDomainClique statuses.
  # 2. The CD controller in the new namespace can sync with those statuses.
  # 3. Resources in the old namespace are properly cleaned up on unprepare.

  local _old_ns="gpu-operator"
  local _new_ns="nvidia-dra-driver-gpu"
  local _spec="tests/bats/specs/imex-rct-pod.yaml"
  local _podname="imex-channel-injection"

  # Stage 1: install in old namespace with ComputeDomainCliques feature enabled.
  local _iargs_old=(
    "--set" "logVerbosity=6"
    "--set" "featureGates.ComputeDomainCliques=true"
  )

  helm uninstall "${TEST_HELM_RELEASE_NAME}" -n "${_new_ns}" --wait --timeout=30s || true
  kubectl wait --for=delete pods -A -l app.kubernetes.io/name=nvidia-dra-driver-gpu --timeout=10s || true

  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs_old "${_old_ns}"

  # Stage 2: apply IMEX workload; this creates a ComputeDomain, triggers the
  # IMEX daemon DaemonSet in the old namespace, and calls prepare.
  kubectl apply -f "${_spec}"
  kubectl wait --for=condition=READY pods "${_podname}" --timeout=100s
  run kubectl logs "${_podname}"
  assert_output --partial "channel0"

  # Verify CD status shows nodes and ComputeDomainCliques are created in old ns.
  sleep 4
  run bats_pipe kubectl get computedomain imex-channel-injection -o json \| jq '.status'
  assert_output --partial 'nodes'

  local CD_UID
  CD_UID=$(kubectl get computedomain imex-channel-injection -o jsonpath='{.metadata.uid}')
  log "ComputeDomain UID: ${CD_UID}"

  local clique_count
  clique_count=$(kubectl get computedomaincliques.resource.nvidia.com -n "${_old_ns}" \
    -l "resource.nvidia.com/computeDomain=${CD_UID}" --no-headers 2>/dev/null | wc -l | awk '{print $1}')
  log "ComputeDomainCliques in ${_old_ns}: ${clique_count}"
  [ "${clique_count}" -gt 0 ]

  # Stage 3: switch driver to new namespace, configuring it to also watch the
  # old namespace via ADDITIONAL_NAMESPACES so it can manage the IMEX daemon
  # DaemonSets still running there.
  local _iargs_new=(
    "--set" "logVerbosity=6"
    "--set" "featureGates.ComputeDomainCliques=true"
    "--set" "controller.containers.computeDomain.env[0].name=ADDITIONAL_NAMESPACES"
    "--set" "controller.containers.computeDomain.env[0].value=${_old_ns}"
  )

  helm uninstall "${TEST_HELM_RELEASE_NAME}" -n "${_old_ns}" --wait --timeout=30s
  kubectl wait --for=delete pods -A -l app.kubernetes.io/name=nvidia-dra-driver-gpu --timeout=10s
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs_new

  # Stage 4: verify IMEX daemon DaemonSet is still running in old namespace
  # (the ComputeDomain still exists so it should not have been deleted).
  kubectl get daemonset -n "${_old_ns}" \
    -l "resource.nvidia.com/computeDomain=${CD_UID}" \
    --no-headers | grep -v "^$"

  # Stage 5: verify the CD controller in new namespace syncs with
  # ComputeDomainClique statuses from old namespace.
  sleep 5
  clique_count=$(kubectl get computedomaincliques.resource.nvidia.com -n "${_old_ns}" \
    -l "resource.nvidia.com/computeDomain=${CD_UID}" --no-headers 2>/dev/null | wc -l | awk '{print $1}')
  log "ComputeDomainCliques in ${_old_ns} after ns switch: ${clique_count}"
  [ "${clique_count}" -gt 0 ]

  # CD should still be Ready: the CD controller in the new namespace can see the
  # IMEX daemon (via ADDITIONAL_NAMESPACES) and the ComputeDomainCliques in the
  # old namespace are still being updated.
  run bats_pipe kubectl get computedomain imex-channel-injection -o json \| jq '.status.status'
  assert_output --partial 'Ready'

  # Stage 6: delete workload (triggers unprepare).
  timeout -v 60 kubectl delete -f "${_spec}"
  kubectl wait --for=delete pods "${_podname}" --timeout=30s

  # Stage 7: verify cleanup of resources in old namespace.
  # DaemonSets from old namespace associated with the deleted CD must be gone.
  kubectl wait --for=delete daemonset \
    -n "${_old_ns}" \
    -l "resource.nvidia.com/computeDomain=${CD_UID}" \
    --timeout=30s || true
  run kubectl get daemonset -n "${_old_ns}" \
    -l "resource.nvidia.com/computeDomain=${CD_UID}" -o name 2>/dev/null
  assert_output ""

  # ComputeDomainCliques from old namespace must be cleaned up.
  kubectl wait --for=delete computedomaincliques.resource.nvidia.com \
    -n "${_old_ns}" \
    -l "resource.nvidia.com/computeDomain=${CD_UID}" \
    --timeout=30s || true
  run kubectl get computedomaincliques.resource.nvidia.com -n "${_old_ns}" \
    -l "resource.nvidia.com/computeDomain=${CD_UID}" -o name 2>/dev/null
  assert_output ""

  # Stage 8: fresh workload cycle to verify normal operation in new namespace.
  apply_check_delete_workload_imex_chan_inject
}


# bats test_tags=fastfeedback
@test "namespace-switch: MPS prepare/unprepare cleanup in previous namespace" {
  # This test verifies that when the driver is switched from one namespace to
  # another, the MPS control daemon Deployment that was created in the previous
  # namespace is properly cleaned up when the workload is deleted (unprepare).

  local _old_ns="gpu-operator"
  local _new_ns="nvidia-dra-driver-gpu"
  local _spec="tests/bats/specs/mps-rct-pod.yaml"
  local _podname="pod-mps-gpu"

  # Stage 1: install in old namespace with MPSSupport feature enabled.
  local _iargs_old=(
    "--set" "logVerbosity=6"
    "--set" "featureGates.MPSSupport=true"
  )

  helm uninstall "${TEST_HELM_RELEASE_NAME}" -n "${_new_ns}" --wait --timeout=30s || true
  kubectl wait --for=delete pods -A -l app.kubernetes.io/name=nvidia-dra-driver-gpu --timeout=10s || true

  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs_old "${_old_ns}"

  # Stage 2: apply MPS workload; this triggers an MPS control daemon Deployment
  # in the old namespace.
  kubectl apply -f "${_spec}"
  kubectl wait --for=condition=READY pods "${_podname}" --timeout=100s

  # Verify MPS control daemon Deployment was created in old namespace.
  local mps_deploy_count
  mps_deploy_count=$(kubectl get deployment -n "${_old_ns}" \
    -o name 2>/dev/null | grep -c "mps-control-daemon" || true)
  log "MPS control daemon Deployments in ${_old_ns}: ${mps_deploy_count}"
  [ "${mps_deploy_count}" -gt 0 ]

  # Capture MPS deployment name for later cleanup verification.
  local mps_deploy_name
  mps_deploy_name=$(kubectl get deployment -n "${_old_ns}" \
    -o name 2>/dev/null | grep "mps-control-daemon" | head -n1 | cut -d/ -f2)
  log "MPS control daemon Deployment name: ${mps_deploy_name}"

  # Stage 3: switch driver to new namespace.
  local _iargs_new=(
    "--set" "logVerbosity=6"
    "--set" "featureGates.MPSSupport=true"
  )

  helm uninstall "${TEST_HELM_RELEASE_NAME}" -n "${_old_ns}" --wait --timeout=30s
  kubectl wait --for=delete pods -A -l app.kubernetes.io/name=nvidia-dra-driver-gpu --timeout=10s
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs_new

  # Stage 4: delete workload (triggers unprepare via new kubelet plugin).
  kubectl delete -f "${_spec}"
  kubectl wait --for=delete pods "${_podname}" --timeout=30s

  # Stage 5: verify MPS control daemon Deployment in old namespace is cleaned up.
  # After unprepare, the MPS Deployment from the old namespace must not remain.
  kubectl wait --for=delete deployment "${mps_deploy_name}" \
    -n "${_old_ns}" --timeout=30s || true
  run kubectl get deployment -n "${_old_ns}" "${mps_deploy_name}" --ignore-not-found
  assert_output ""

  # Also verify no lingering MPS control daemon Deployments in old namespace.
  #run kubectl get deployment -n "${_old_ns}" \
  #  --no-headers 2>/dev/null
  #refute_output --partial "mps-control-daemon"

  # Stage 6: fresh MPS workload cycle to verify normal operation in new namespace.
  kubectl apply -f "${_spec}"
  kubectl wait --for=condition=READY pods "${_podname}" --timeout=60s

  # MPS Deployment should now be in new namespace.
  mps_deploy_count=$(kubectl get deployment -n "${_new_ns}" \
    -o name 2>/dev/null | grep -c "mps-control-daemon" || true)
  log "MPS control daemon Deployments in ${_new_ns}: ${mps_deploy_count}"
  [ "${mps_deploy_count}" -gt 0 ]

  # Capture new-namespace MPS deployment name for cleanup verification.
  local mps_new_deploy_name
  mps_new_deploy_name=$(kubectl get deployment -n "${_new_ns}" \
    -o name 2>/dev/null | grep "mps-control-daemon" | head -n1 | cut -d/ -f2)
  log "MPS control daemon Deployment name in ${_new_ns}: ${mps_new_deploy_name}"

  kubectl delete -f "${_spec}"
  kubectl wait --for=delete pods "${_podname}" --timeout=30s

  # MPS Deployment in new namespace should be cleaned up after workload deletion.
  kubectl wait --for=delete deployment "${mps_new_deploy_name}" \
    -n "${_new_ns}" --timeout=30s || true
  run kubectl get deployment -n "${_new_ns}" \
    --no-headers 2>/dev/null
  refute_output --partial "mps-control-daemon"
}
