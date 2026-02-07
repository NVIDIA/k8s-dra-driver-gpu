# shellcheck disable=SC2148
# shellcheck disable=SC2329

setup_file () {
  load 'helpers.sh'
  _common_setup
  local _iargs=("--set" "logVerbosity=6")
  iupgrade_wait "${TEST_CHART_REPO}" "${TEST_CHART_VERSION}" _iargs
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
  show_gpu_plugin_log_tails
  echo -e "FAILURE HOOK END\n\n"
}


# bats test_tags=extres,fastfeedback
@test "GPUs: handle legacy 'nvidia.com/gpu: 1' (with DRAExtendedResource)" {
  # 1) Skip test if criteria are not met.
  run kubectl version -o json
  assert_success

  local k8s_version=$(echo "$output" | jq -r '.serverVersion.gitVersion')
  local major=$(echo "$output" | jq -r '.serverVersion.major')
  local minor=$(echo "$output" | jq -r '.serverVersion.minor' | sed 's/+$//')

  if [[ "$major" -lt 1 ]] || [[ "$major" -eq 1 && "$minor" -lt 35 ]]; then
    skip "Kubernetes version ${k8s_version} is < 1.35, DRAExtendedResource not available"
  fi

  run kubectl get pod -n kube-system -l component=kube-apiserver -o yaml
  assert_success

  if ! echo "$output" | grep -q "DRAExtendedResource=true"; then
    skip "DRAExtendedResource=true not found in API server pod spec"
  fi

  # 2) Spot-check that GPUs are not announced via device plugin.
  local node_name=$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}')
  run kubectl get node "${node_name}" -o jsonpath='{.status.capacity.nvidia\.com/gpu}'

  if [[ -n "$output" && "$output" != "0" ]]; then
    echo "ERROR: Node ${node_name} has nvidia.com/gpu capacity: ${output}"
    echo "The NVIDIA device plugin must be disabled"
    return 1
  fi

  local pod_name="test-gpu-extres-$(date +%s)"

  # The inline spec uses TAB indentation! :)
  kubectl apply -f - <<-EOF
		apiVersion: v1
		kind: Pod
		metadata:
		  name: ${pod_name}
		  labels:
		    env: batssuite
		spec:
		  restartPolicy: Never
		  containers:
		  - name: cnt
		    image: ubuntu:24.04
		    command: ["sh", "-c", "nvidia-smi -L && sleep infinity"]
		    resources:
		      limits:
		        nvidia.com/gpu: "1"
		      requests:
		        nvidia.com/gpu: "1"
	EOF

  # 3) explicitly confirm that GPU was injected
  kubectl wait --for=condition=Ready "pods/${pod_name}" --timeout=20s
  run kubectl logs "${pod_name}"
  assert_success
  assert_output --partial "GPU-"

  # 4) explicitly confirm that a resource claim was auto-created
  run kubectl get resourceclaims -o json
  assert_success

  local claim_name=$(echo "$output" | jq -r ".items[] | select(.metadata.name | startswith(\"${pod_name}-extended-resources-\")) | .metadata.name")
  kubectl get resourceclaim "${claim_name}" -o yaml

  run kubectl get resourceclaim "${claim_name}" -o jsonpath='{.metadata.annotations.resource\.kubernetes\.io/extended-resource-claim}'
  assert_output "true"

  run kubectl get resourceclaim "${claim_name}" -o jsonpath='{.status.allocation.devices.results[0].driver}'
  assert_output "gpu.nvidia.com"

  kubectl delete pod "${pod_name}"
  kubectl wait --for=delete "pods/${pod_name}" --timeout=10s
}
