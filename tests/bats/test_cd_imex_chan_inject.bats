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


@test "IMEX channel injection (single)" {
  log_objects
  apply_check_delete_workload_imex_chan_inject
}


# bats test_tags=fastfeedback
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
  kubectl wait --for=delete pods imex-channel-injection-all --timeout=10s
}
