# shellcheck disable=SC2148
# shellcheck disable=SC2329

: "${STRESS_PODS_N:=15}"
: "${STRESS_LOOPS:=5}"
: "${STRESS_DELAY:=30}"

setup_file () {
  load 'helpers.sh'
  _common_setup
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

# Expand pod YAML with indexes
_generate_pods_manifest() {
  local out="$1"
  local template="tests/bats/specs/pods-shared-gpu.yaml"
  : > "$out"
  for i in $(seq 1 "${STRESS_PODS_N}"); do
    sed "s/__INDEX__/${i}/g" "${template}" >> "$out"
    echo "---" >> "$out"
  done
}

@test "Stress: shared ResourceClaim across ${STRESS_PODS_N} pods x ${STRESS_LOOPS} loops" {
  for loop in $(seq 1 "${STRESS_LOOPS}"); do
    echo "=== Loop $loop/${STRESS_LOOPS} ==="

    # Apply ResourceClaim
    kubectl apply -f tests/bats/specs/rc-shared-gpu.yaml

    # Generate and apply pods spec
    manifest="${BATS_TEST_TMPDIR:-/tmp}/pods-shared-${loop}.yaml"
    _generate_pods_manifest "$manifest"
    kubectl apply  -f "$manifest"

    # Wait for ResourceClaim allocation
    kubectl wait --for=jsonpath='{.status.allocation}' resourceclaim rc-shared-gpu --timeout=120s

    # Wait for all pods to be Ready
    kubectl wait  --for=condition=Ready pods -l 'env=batssuite,test=stress-shared' --timeout=180s

    # Verify pod phases
    phases=$(kubectl get pods  -l 'env=batssuite,test=stress-shared' -o jsonpath='{range .items[*]}{.metadata.name}{" "}{.status.phase}{"\n"}{end}')
    echo "$phases"
    echo "$phases" | awk '$2!="Running"{exit 1}'

    # Spot-check GPU allocation logs
    run kubectl logs  stress-pod-1
    assert_output --partial "UUID: GPU-"

    # Cleanup
    kubectl delete pods  -l 'env=batssuite,test=stress-shared' --timeout=90s
    kubectl delete -f tests/bats/specs/rc-shared-gpu.yaml --timeout=90s
    kubectl wait  --for=delete pods -l 'env=batssuite,test=stress-shared' --timeout=60s

    if [[ "$loop" -lt "$STRESS_LOOPS" ]]; then
      echo "Sleeping ${STRESS_DELAY}s before next loop..."
      sleep "${STRESS_DELAY}"
    fi
  done
}
