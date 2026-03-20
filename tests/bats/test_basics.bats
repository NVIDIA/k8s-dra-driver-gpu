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

# A test that covers local dev tooling; we don't want to
# unintentionally change/break these targets.
@test "basics: test VERSION_W_COMMIT, VERSION_GHCR_CHART, VERSION" {
  run make print-VERSION
  assert_output --regexp '^v[0-9]+\.[0-9]+\.[0-9]+-dev$'
  run make print-VERSION_W_COMMIT
  assert_output --regexp '^v[0-9]+\.[0-9]+\.[0-9]+-dev-[0-9a-f]{8}$'
  run make print-VERSION_GHCR_CHART
  assert_output --regexp '^[0-9]+\.[0-9]+\.[0-9]+-dev-[0-9a-f]{8}-chart$'
}


@test "basics: confirm no kubelet plugin pods running" {
  run kubectl get pods -A -l nvidia-dra-driver-gpu-component=kubelet-plugin
  [ "$status" -eq 0 ]
  refute_output --partial 'Running'
}

# Make it explicit when major dependency is missing
@test "basics: GPU Operator installed" {
  run helm list -A
  assert_output --partial 'gpu-operator'
}

@test "basics: helm-install ${TEST_CHART_REPO}/${TEST_CHART_VERSION}" {
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" NOARGS
}


@test "basics: helm list: validate output" {
  # Sanity check: one chart installed.
  helm list -n nvidia-dra-driver-gpu -o json | jq 'length == 1'

  # Confirm consistency between the various version-related parameters. Note
  # that the --version arg provided to `helm install/upgrade` does not directly
  # set app_version; it is just a version constraint. `app_version` tested here
  # is AFAIU defined solely by the chart's appVersion YAML spec.
  helm list -n nvidia-dra-driver-gpu -o json | jq '.[].app_version' | grep "${TEST_CHART_VERSION}"
}


@test "basics: get crd computedomains.resource.nvidia.com" {
  kubectl get crd computedomains.resource.nvidia.com
}


@test "basics: wait for plugin & controller pods READY" {
  kubectl wait --for=condition=READY pods -A \
    -l nvidia-dra-driver-gpu-component=kubelet-plugin --timeout=10s
  kubectl wait --for=condition=READY pods -A \
    -l nvidia-dra-driver-gpu-component=controller --timeout=10s
}


@test "basics: validate CD controller container image spec" {
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


@test "basics: SIGUSR2 handler: GPU plugin, CD plugin" {
  local PNAME="$(get_one_kubelet_plugin_pod_name)"
  # Assume that GPU plugin has PID 1.
  kubectl exec -n nvidia-dra-driver-gpu "${PNAME}" -c gpus -- kill -s SIGUSR2 1
  run kubectl exec -n nvidia-dra-driver-gpu "${PNAME}" -c gpus -- cat /tmp/goroutine-stacks.dump
  assert_output --partial 'main.RunPlugin'

  kubectl exec -n nvidia-dra-driver-gpu "${PNAME}" -c compute-domains -- kill -s SIGUSR2 1
  run kubectl exec -n nvidia-dra-driver-gpu "${PNAME}" -c compute-domains -- cat /tmp/goroutine-stacks.dump
  assert_output --partial 'main.RunPlugin'
}


# bats test_tags=fastfeedback
@test "basics: upgrade: install-last-stable and upgrade-to-current-dev in gpu-operator ns with ADDITIONAL_NAMESPACES" {
  # This test covers a GPU Operator-style deployment where the DRA driver runs
  # in the gpu-operator namespace and watches nvidia-dra-driver-gpu for
  # workloads via the ADDITIONAL_NAMESPACES env var.
  local _iargs=(
    "--set" "controller.containers.computeDomain.env[0].name=ADDITIONAL_NAMESPACES"
    "--set" "controller.containers.computeDomain.env[0].value=nvidia-dra-driver-gpu"
  )

  # Uninstall from nvidia-dra-driver-gpu namespace (state from previous tests).
  helm uninstall "${TEST_HELM_RELEASE_NAME}" -n nvidia-dra-driver-gpu --wait --timeout=30s
  kubectl wait --for=delete pods -A -l app.kubernetes.io/name=nvidia-dra-driver-gpu --timeout=10s

  # Install last-stable to gpu-operator namespace with ADDITIONAL_NAMESPACES.
  iupgrade_wait "${TEST_CHART_LASTSTABLE_REPO}" "${TEST_CHART_LASTSTABLE_VERSION}" _iargs gpu-operator

  # Update CRD
  kubectl apply -f "${CRD_UPGRADE_URL}"

  # Upgrade to current-dev, still in gpu-operator namespace.
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs gpu-operator

  # Verify CRD is present
  kubectl get crd computedomains.resource.nvidia.com

  # Restore state: uninstall from gpu-operator, reinstall to nvidia-dra-driver-gpu.
  helm uninstall "${TEST_HELM_RELEASE_NAME}" -n gpu-operator --wait --timeout=30s
  kubectl wait --for=delete pods -A -l app.kubernetes.io/name=nvidia-dra-driver-gpu --timeout=10s
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" NOARGS
}
