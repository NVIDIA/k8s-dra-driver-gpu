# shellcheck disable=SC2148
# shellcheck disable=SC2329

setup_file() {
  load 'helpers.sh'
  _common_setup

  local _iargs=("--set" "logVerbosity=6")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs
}

teardown_file() {
  kubectl delete -f tests/bats/specs/nvb2.yaml --ignore-not-found
  kubectl wait --for=delete job/test-failover-job-launcher --timeout=20s || true
}


@test "CD failover nvb2: force-delete worker pod 0" {
  bash tests/bats/lib/test_cd_nvb_failover.sh tests/bats/specs/nvb2.yaml 1
}


@test "CD failover nvb2: force-delete all IMEX daemons" {
  bash tests/bats/lib/test_cd_nvb_failover.sh tests/bats/specs/nvb2.yaml 2
}


@test "CD failover nvb2: regular-delete worker pod 1" {
  bash tests/bats/lib/test_cd_nvb_failover.sh tests/bats/specs/nvb2.yaml 3
}
