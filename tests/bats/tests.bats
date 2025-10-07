# shellcheck disable=SC2148
# shellcheck disable=SC2329
setup() {
  # Executed before entering each test in this file.
   load 'helpers.sh'
  _common_setup
}


# Currently, the tests defined in this file deliberately depend on each other
# and are expected to execute in the order defined. In the future, we want to
# build test dependency injection (with fixtures), and work towards clean
# _isolation_ between tests. To that end, we will hopefully find fast and
# reliable strategies to conceptually prevent cross-contamination from
# happening. Tools like `etcdctl` will be helpful.


# Note(JP): bats swallows output of setup upon success (regardless of cmdline
# args such as `--show-output-of-passing-tests`). Ref:
# https://github.com/bats-core/bats-core/issues/540#issuecomment-1013521656 --
# it however does emit output upon failure.
# shellcheck disable=SC2329
setup_file() {
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

apply_check_delete_workload_imex_chan_inject() {
  kubectl apply -f demo/specs/imex/channel-injection.yaml
  kubectl wait --for=condition=READY pods imex-channel-injection --timeout=100s
  run kubectl logs imex-channel-injection
  kubectl delete -f demo/specs/imex/channel-injection.yaml
  # Check output after attempted deletion.
  assert_output --partial "channel0"

  # Wait for deletion to complete; this is critical before moving on to the next
  # test (as long as we don't wipe state entirely between tests).
  kubectl wait --for=delete pods imex-channel-injection --timeout=10s
}

log_objects() {
  # Never fail, but show output in case a test fails, to facilitate debugging.
  # Could this be part of setup()? If setup succeeds and when a test fails:
  # does this show the output of setup? Then we could do this.
  kubectl get resourceclaims || true
  kubectl get computedomain || true
  kubectl get pods -o wide || true
}

# A test that covers local dev tooling, we don't want to
# unintentionally change/break these targets.
@test "test VERSION_W_COMMIT, VERSION_GHCR_CHART, VERSION" {
  run make print-VERSION
  assert_output --regexp '^v[0-9]+\.[0-9]+\.[0-9]+-dev$'
  run make print-VERSION_W_COMMIT
  assert_output --regexp '^v[0-9]+\.[0-9]+\.[0-9]+-dev-[0-9a-f]{8}$'
  run make print-VERSION_GHCR_CHART
  assert_output --regexp '^[0-9]+\.[0-9]+\.[0-9]+-dev-[0-9a-f]{8}-chart$'
}

@test "confirm no kubelet plugin pods running" {
  run kubectl get pods -A -l nvidia-dra-driver-gpu-component=kubelet-plugin
  [ "$status" -eq 0 ]
  refute_output --partial 'Running'
}

@test "helm-install ${TEST_CHART_REPO}/${TEST_CHART_VERSION}" {
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" NOARGS
}

@test "helm list: validate output" {
  # Sanity check: one chart installed.
  helm list -n nvidia-dra-driver-gpu -o json | jq 'length == 1'

  # Confirm consistency between the various version-related parameters. Note
  # that the --version arg provided to `helm install/upgrade` does not directly
  # set app_version; it is just a version constraint. `app_version` tested here
  # is AFAIU defined solely by the chart's appVersion YAML spec.
  helm list -n nvidia-dra-driver-gpu -o json | jq '.[].app_version' | grep "${TEST_CHART_VERSION}"
}

@test "get crd computedomains.resource.nvidia.com" {
  kubectl get crd computedomains.resource.nvidia.com
}

@test "wait for kubelet plugin pods READY" {
  kubectl wait --for=condition=READY pods -A \
    -l nvidia-dra-driver-gpu-component=kubelet-plugin --timeout=10s
}

@test "wait for controller pod READY" {
  kubectl wait --for=condition=READY pods -A \
    -l nvidia-dra-driver-gpu-component=controller --timeout=10s
}

@test "validate CD controller container image spec" {
  local ACTUAL_IMAGE_SPEC
  ACTUAL_IMAGE_SPEC=$(kubectl get pod \
    -n nvidia-dra-driver-gpu \
    -l nvidia-dra-driver-gpu-component=controller \
    -o json | \
      jq -r '.items[].spec.containers[] | select(.name=="compute-domain") | .image')

  # Emit once, unfiltered, for debuggability
  echo "$ACTUAL_IMAGE_SPEC"

  # Confirm substring; TODO: make tighter with precise
  # TEST_EXPECTED_IMAGE_SPEC_SUBSTRING
  echo "$ACTUAL_IMAGE_SPEC" | grep "${TEST_EXPECTED_IMAGE_SPEC_SUBSTRING}"
}

@test "IMEX channel injection (single)" {
  log_objects
  apply_check_delete_workload_imex_chan_inject
}

@test "IMEX channel injection (all)" {
  log_objects
  # Example: with TEST_CHART_VERSION="v25.3.2-12390-chart"
  # the command below returns 0 (true: the tested version is smaller)
  if dpkg --compare-versions "${TEST_CHART_VERSION#v}" lt "25.8.0"; then
    skip "tested chart version smaller than 25.8.0"
  fi
  kubectl apply -f demo/specs/imex/channel-injection-all.yaml
  kubectl wait --for=condition=READY pods imex-channel-injection-all --timeout=80s
  run kubectl logs imex-channel-injection-all
  assert_output --partial "channel2047"
  assert_output --partial "channel222"
  kubectl delete -f demo/specs/imex/channel-injection-all.yaml
}

@test "CD daemon shutdown: confirm CD status cleanup" {
  log_objects

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
  cat "$LOGPATH" | grep -e "Successfully updated node .* status to NotReady"
  cat "$LOGPATH" | grep "Successfully removed node" | \
    grep "from ComputeDomain default/imex-channel-injection"

  # Delete CD.
  kubectl delete computedomain imex-channel-injection
}

@test "NodePrepareResources: catch unknown field in opaque cfg in ResourceClaim" {
  log_objects

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
}

@test "nickelpie (NCCL send/recv/broadcast, 2 pods, 2 nodes, small payload)" {
  log_objects

  # Do not run in checkout dir (to not pollute that).
  cd "${BATS_TEST_TMPDIR}"
  git clone https://github.com/jgehrcke/jpsnips-nv
  cd jpsnips-nv && git checkout fb46298fc7aa5fc1322b4672e8847da5321baeb7
  cd nickelpie/one-pod-per-node/
  bash teardown-start-evaluate-npie-job.sh --gb-per-benchmark 5 --matrix-scale 2 --n-ranks 2
  run kubectl logs --prefix -l job-name=nickelpie-test --tail=-1
  kubectl wait --for=condition=complete --timeout=60s job/nickelpie-test
  kubectl delete -f npie-job.yaml.rendered
  kubectl wait --for=delete  --timeout=60s job/nickelpie-test
  echo "${output}" | grep -E '^.*broadcast-.*RESULT bandwidth: [0-9]+\.[0-9]+ GB/s.*$'
}

@test "nvbandwidth (2 nodes, 2 GPUs each)" {
  log_objects

  kubectl create -f https://github.com/kubeflow/mpi-operator/releases/download/v0.6.0/mpi-operator.yaml || echo "ignore"
  kubectl apply -f demo/specs/imex/nvbandwidth-test-job-1.yaml
  # The canonical k8s job interface works even for MPIJob (the MPIJob has an
  # underlying k8s job).
  kubectl wait --for=create job/nvbandwidth-test-1-launcher --timeout=20s
  kubectl wait --for=condition=complete job/nvbandwidth-test-1-launcher --timeout=60s
  run kubectl logs --tail=-1 --prefix -l job-name=nvbandwidth-test-1-launcher
  kubectl delete -f demo/specs/imex/nvbandwidth-test-job-1.yaml
  echo "${output}" | grep -E '^.*SUM multinode_device_to_device_memcpy_read_ce [0-9]+\.[0-9]+.*$'
}

@test "downgrade: current-dev -> last-stable" {
  log_objects

  # Stage 1: apply workload, but do not delete.
  kubectl apply -f demo/specs/imex/channel-injection.yaml
  kubectl wait --for=condition=READY pods imex-channel-injection --timeout=60s
  run kubectl logs imex-channel-injection
  assert_output --partial "channel0"

  # Stage 2: upgrade as users would do (explicitly not downgrade the CRD).
  iupgrade_wait "$TEST_CHART_LASTSTABLE_REPO" "$TEST_CHART_LASTSTABLE_VERSION" NOARGS

  # Stage 3: confirm workload deletion works post-upgrade.
  timeout -v 80 kubectl delete -f demo/specs/imex/channel-injection.yaml
  kubectl wait --for=delete pods imex-channel-injection --timeout=10s

  # Stage 4: fresh create-confirm-delete workload cycle.
  apply_check_delete_workload_imex_chan_inject
}

@test "upgrade: wipe-state, install-last-stable, upgrade-to-current-dev" {
  log_objects

  # Stage 1: clean slate
  helm uninstall "${TEST_HELM_RELEASE_NAME}" -n nvidia-dra-driver-gpu --wait --timeout=30s
  kubectl wait --for=delete pods -A -l app.kubernetes.io/name=nvidia-dra-driver-gpu --timeout=10s
  bash tests/bats/clean-state-dirs-all-nodes.sh
  kubectl get crd computedomains.resource.nvidia.com
  timeout -v 10 kubectl delete crd computedomains.resource.nvidia.com

  # Stage 2: install last-stable (this guarantees to install the "old" CRD)
  iupgrade_wait "${TEST_CHART_LASTSTABLE_REPO}" "${TEST_CHART_LASTSTABLE_VERSION}" NOARGS

  # Stage 3: apply workload, do not delete (users care about old workloads to
  # ideally still run, but at the very least be deletable after upgrade).
  kubectl apply -f demo/specs/imex/channel-injection.yaml
  kubectl wait --for=condition=READY pods imex-channel-injection --timeout=60s
  run kubectl logs imex-channel-injection
  assert_output --partial "channel0"

  # Stage 4: update CRD, as a user would do.
  kubectl apply -f "${CRD_UPGRADE_URL}"

  # Stage 5: install target version (as users would do).
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" NOARGS

  # Stage 6: confirm deleting old workload works (critical, see above).
  timeout -v 60 kubectl delete -f demo/specs/imex/channel-injection.yaml
  kubectl wait --for=delete pods imex-channel-injection --timeout=60s

  # Stage 7: fresh create-confirm-delete workload cycle.
  apply_check_delete_workload_imex_chan_inject
}
