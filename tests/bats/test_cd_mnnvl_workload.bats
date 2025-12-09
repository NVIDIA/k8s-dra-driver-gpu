# shellcheck disable=SC2148
# shellcheck disable=SC2329

setup_file() {
  load 'helpers.sh'
  local _iargs=("--set" "logVerbosity=6")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs
}


setup () {
  load 'helpers.sh'
  _common_setup
  log_objects
}

# bats test_tags=fastfeedback
@test "nickelpie (NCCL send/recv/broadcast, 2 pods, 2 nodes, small payload)" {
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

# The MPI operator dependency is slightly heavy for CI, pulling the image was
# seen to take long. Maybe make this a prerequisite (to be pre-installed).
@test "nvbandwidth (2 nodes, 2 GPUs each)" {
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
