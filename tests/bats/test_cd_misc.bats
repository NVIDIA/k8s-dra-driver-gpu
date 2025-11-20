# shellcheck disable=SC2148
# shellcheck disable=SC2329

# Currently, the tests defined in this file deliberately depend on each other
# and are expected to execute in the order defined. In the future, we want to
# build test dependency injection (with fixtures), and work towards clean
# _isolation_ between tests. To that end, we will hopefully find fast and
# reliable strategies to conceptually prevent cross-contamination from
# happening. Tools like `etcdctl` will be helpful.

# Hint: when developing/testing a specific test, for faster iteration add the
# comment `# bats test_tags=bats:focus` on top of the current test. This can
# help (requires the test to be self-contained though, i.e. it sets up its
# dependencies' does not rely on a previous test for that).

# Note(JP): bats swallows output of setup upon success (regardless of cmdline
# args such as `--show-output-of-passing-tests`). Ref:
# https://github.com/bats-core/bats-core/issues/540#issuecomment-1013521656 --
# it however does emit output upon failure.


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
  get_all_cd_daemon_logs_for_cd_name "imex-channel-injection" || true
  echo -e "FAILURE HOOK END\n\n"
}


@test "CD daemon shutdown: confirm CD status cleanup" {
  kubectl apply -f demo/specs/imex/channel-injection.yaml
  kubectl wait --for=condition=READY pods imex-channel-injection --timeout=100s
  run kubectl logs imex-channel-injection
  assert_output --partial "channel0"

  local LOGPATH="${BATS_TEST_TMPDIR}/cd-daemon.log"
  local PNAME
  PNAME=$( \
    kubectl get pods -n nvidia-dra-driver-gpu | \
    grep imex-channel-injection | \
    awk '{print $1}'
  )

  # Expect `nodes` key to be present in CD status.
  run bats_pipe kubectl get computedomain imex-channel-injection -o json \| jq '.status'
  assert_output --partial 'nodes'

  echo "attach background log follower to daemon pod: $PNAME"
  kubectl logs -n nvidia-dra-driver-gpu --follow "$PNAME" > "$LOGPATH" 2>&1 &
  kubectl delete pods imex-channel-injection

  # Note: the log follower child process terminates when the pod terminates.
  kubectl wait --for=delete pods imex-channel-injection --timeout=10s

  # Expect `nodes` key to not be be present (single-node CD).
  run bats_pipe kubectl get computedomain imex-channel-injection -o json \| jq '.status'
  refute_output --partial 'nodes'

  # Inspect CD daemon log, dump tail for easier debug-on-failure.
  cat "$LOGPATH" | tail -n 50

  # Explicitly confirm cleanup-on-shutdown behavior by inspecting CD log.
  cat "$LOGPATH" | tail -n 50 | grep -e "updated node status in CD (new nodeinfo: .* NotReady"
  cat "$LOGPATH" | grep "Successfully removed node" | \
    grep "from ComputeDomain default/imex-channel-injection"

  # Delete CD.
  kubectl delete computedomain imex-channel-injection
}

@test "reject unknown field in opaque cfg in CD chan ResourceClaim" {
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" NOARGS

  envsubst < tests/bats/specs/rc-opaque-cfg-unknown-field.yaml.tmpl > \
    "${BATS_TEST_TMPDIR}"/rc-opaque-cfg-unknown-field.yaml
  cd "${BATS_TEST_TMPDIR}"

  local SPEC="rc-opaque-cfg-unknown-field.yaml"

  # Create pod with random name suffix.
  # Store ref of the form `pod/batssuite-pod-boc-brs2l`.
  local POD
  POD=$(kubectl create -f "${SPEC}" | grep pod | awk '{print $1;}')

  # Confirm ContainerCreating state (no failure yet though).
  kubectl wait \
    --for=jsonpath='{.status.containerStatuses[0].state.waiting.reason}'=ContainerCreating \
    --timeout=10s \
    "${POD}"

  # Rather quickly, we expect an event with reason
  # `FailedPrepareDynamicResources`. That's not typically the method users
  # discover the error.
  wait_for_pod_event "${POD}" FailedPrepareDynamicResources 10

  # This is how users probably see this error first.
  kubectl describe "${POD}" | grep FailedPrepareDynamicResources | \
    grep "error preparing devices"  | \
    grep 'strict decoding error: unknown field "unexpectedField"'

  # Confirm that precise root cause can also be inferred from
  # CD kubelet plugin logs.
  kubectl logs \
    -l nvidia-dra-driver-gpu-component=kubelet-plugin \
    -n nvidia-dra-driver-gpu \
    --prefix --tail=-1 | \
      grep 'Permanent error' | \
      grep 'strict decoding error: unknown field "unexpectedField"'

  # Clean up.
  kubectl delete "${POD}"
  kubectl delete resourceclaim batssuite-rc-bad-opaque-config
  kubectl wait --for=delete "${POD}" --timeout=10s
}

@test "self-initiated unprepare of stale RCs in PrepareStarted" {
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" NOARGS

  # Stage 1: provoke partially prepared claim.
  #
  # Based on the "catch unknown field in opaque cfg in ResourceClaim" test
  # above: Provoke a permanent Prepare() error, leaving behind a partially
  # prepared claim in the checkpoint.
  envsubst < tests/bats/specs/rc-opaque-cfg-unknown-field.yaml.tmpl > \
    "${BATS_TEST_TMPDIR}"/rc-opaque-cfg-unknown-field.yaml
  local SPEC="${BATS_TEST_TMPDIR}/rc-opaque-cfg-unknown-field.yaml"
  local POD
  POD=$(kubectl create -f "${SPEC}" | grep pod | awk '{print $1;}')
  kubectl wait \
    --for=jsonpath='{.status.containerStatuses[0].state.waiting.reason}'=ContainerCreating \
    --timeout=10s \
    "${POD}"
  wait_for_pod_event "${POD}" FailedPrepareDynamicResources 10
  run kubectl logs \
    -l nvidia-dra-driver-gpu-component=kubelet-plugin \
    -n nvidia-dra-driver-gpu \
    --prefix --tail=-1
  assert_output --partial 'strict decoding error: unknown field "unexpectedField"'

  # Stage 2: test that cleanup routine leaves this claim alone ('not stale')
  #
  # Re-install, flip log verbosity just to enforce container restart. This
  # ensures that the cleanup runs immediately (it runs upon startup, and then
  # only N minutes later again).
  local _iargs=("--set" "logVerbosity=5")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs
  sleep 1   # give the on-startup cleanup a chance to run.
  run kubectl logs \
    -l nvidia-dra-driver-gpu-component=kubelet-plugin \
    -n nvidia-dra-driver-gpu \
    --prefix --tail=-1
  assert_output --partial "partially prepared claim not stale: default/batssuite-rc-bad-opaque-config"

  # Stage 3: simulate stale claim, test cleanup.
  #
  # To that end, uninstall the driver and then remove both pod and RC from the API server.
  # Then, re-install DRA driver and confirm detection and removal of stale claim.
  helm uninstall -n nvidia-dra-driver-gpu nvidia-dra-driver-gpu-batssuite --wait
  kubectl delete "${POD}" --force
  kubectl delete resourceclaim batssuite-rc-bad-opaque-config
  local _iargs=("--set" "logVerbosity=6")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs
  sleep 1  # give the on-startup cleanup a chance to run.

  run kubectl logs \
    -l nvidia-dra-driver-gpu-component=kubelet-plugin \
    -n nvidia-dra-driver-gpu \
    --prefix --tail=-1
  assert_output --partial "Deleted claim from checkpoint: default/batssuite-rc-bad-opaque-config"
  assert_output --partial "Checkpointed RC cleanup: unprepared stale claim: default/batssuite-rc-bad-opaque-config"

  # Stage 4: appendix -- happens shortly thereafter: we do get a
  # UnprepareResourceClaims() call for this claim. Why? It's a noop because the
  # cleanup above was faster.
  sleep 4
  run kubectl logs \
    -l nvidia-dra-driver-gpu-component=kubelet-plugin \
    -n nvidia-dra-driver-gpu \
    --prefix --tail=-1
  assert_output --partial "Unprepare noop: claim not found in checkpoint data"
}
