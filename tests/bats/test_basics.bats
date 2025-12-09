# shellcheck disable=SC2148
# shellcheck disable=SC2329

setup() {
   load 'helpers.sh'
  _common_setup
}

bats::on_failure() {
  echo -e "\n\nFAILURE HOOK START"
  kubectl get pods -A | grep dra
  echo -e "FAILURE HOOK END\n\n"
}

# bats file_tags=fastfeedback
@test "confirm no kubelet plugin pods running" {
  run kubectl get pods -A -l nvidia-dra-driver-gpu-component=kubelet-plugin
  [ "$status" -eq 0 ]
  refute_output --partial 'Running'
}

# Make it explicit when major dependency is missing
@test "GPU Operator installed" {
  run helm list -A
  assert_output --partial 'gpu-operator'
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


@test "wait for plugin & controller pods READY" {
  kubectl wait --for=condition=READY pods -A \
    -l nvidia-dra-driver-gpu-component=kubelet-plugin --timeout=10s
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
