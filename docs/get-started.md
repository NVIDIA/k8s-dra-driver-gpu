# Get started

This page walks through installing the DRA Driver for NVIDIA GPUs and validating that GPU or ComputeDomain allocation is working correctly on your cluster.

---

## Prerequisites

| Requirement | Details |
|---|---|
| Kubernetes | v1.32 or later |
| Helm | v3 or later |
| DRA feature gate | Enabled by default in Kubernetes 1.34+. On 1.32 and 1.33, must be enabled manually — refer to [Enable DRA on Kubernetes 1.32 and 1.33](#enable-dra-on-kubernetes-132-and-133) |
| Container Device Interface (CDI) | Enabled in your container runtime (containerd or CRI-O). Refer to your container runtime documentation for details on enabling this feature. |
| NVIDIA GPU driver | Version 565 or later |

If you plan to use ComputeDomains, you also need:

| Requirement | Details |
|---|---|
| Multi-Node NVLink (MNNVL) hardware | Nodes connected via NVLink fabric, such as GB200 NVL72 or H100 NVLink configurations |
| [GPU Feature Discovery](https://github.com/NVIDIA/gpu-feature-discovery) | Deployed and healthy in your cluster. This generates the `nvidia.com/gpu.clique` node labels required by ComputeDomains |
| `nvidia-imex-*` packages (if installed) | The `nvidia-imex.service` systemd unit must be disabled on all GPU nodes: `systemctl disable --now nvidia-imex.service && systemctl mask nvidia-imex.service` |

---

## Install

1. Add the Helm repository:

    ```bash
    helm repo add nvidia https://helm.ngc.nvidia.com/nvidia && helm repo update
    ```

2. Install the driver. Choose the command that matches your use case.

    **GPU resource allocation**:

    With an operator-managed GPU driver (driver running in a container):

    ```bash
    helm install nvidia-dra-driver-gpu nvidia/nvidia-dra-driver-gpu \
        --version=v25.12.0 \
        --create-namespace \
        --namespace nvidia-dra-driver-gpu \
        --set gpuResourcesEnabledOverride=true \
        --set nvidiaDriverRoot=/run/nvidia/driver
    ```

    With a host-provided GPU driver (driver installed directly on the host):

    ```bash
    helm install nvidia-dra-driver-gpu nvidia/nvidia-dra-driver-gpu \
        --version=v25.12.0 \
        --create-namespace \
        --namespace nvidia-dra-driver-gpu \
        --set gpuResourcesEnabledOverride=true
    ```

    `gpuResourcesEnabledOverride=true` is required because the GPU kubelet plugin cannot safely run alongside the standard NVIDIA GPU device plugin on the same node. Ensure the device plugin DaemonSet is removed or disabled before using this flag.

    **ComputeDomains only** (NVLink fabric workloads, no GPU resource allocation):

    With an operator-managed GPU driver:

    ```bash
    helm install nvidia-dra-driver-gpu nvidia/nvidia-dra-driver-gpu \
        --version=v25.12.0 \
        --create-namespace \
        --namespace nvidia-dra-driver-gpu \
        --set resources.gpus.enabled=false \
        --set nvidiaDriverRoot=/run/nvidia/driver
    ```

    With a host-provided GPU driver:

    ```bash
    helm install nvidia-dra-driver-gpu nvidia/nvidia-dra-driver-gpu \
        --version=v25.12.0 \
        --create-namespace \
        --namespace nvidia-dra-driver-gpu \
        --set resources.gpus.enabled=false
    ```

---

## Configure

The following parameters are most commonly set at install time.

| Parameter | Default | Description |
|---|---|---|
| `nvidiaDriverRoot` | `/` | Path to the GPU driver root on the host. Set to `/run/nvidia/driver` for operator-managed drivers. Incorrect values are a common source of error — see [Troubleshooting](troubleshooting.md). |
| `resources.gpus.enabled` | `true` | Enable the GPU kubelet plugin. Requires `gpuResourcesEnabledOverride=true`. |
| `resources.computeDomains.enabled` | `true` | Enable the ComputeDomain controller and kubelet plugin. |
| `gpuResourcesEnabledOverride` | `false` | Required acknowledgement when enabling GPU resources. See the Install section above. |
| `featureGates` | `{}` | Map of feature gate name to boolean. See [Feature gates](reference/feature-gates.md). |
| `logVerbosity` | `4` | Log verbosity level (0–7). Higher values produce more output. |

To list all available parameters:

```bash
helm show values nvidia/nvidia-dra-driver-gpu
```

---

## Optional: admission webhook

The admission webhook validates opaque configuration in `ResourceClaim` and `ResourceClaimTemplate` specs, providing early feedback on invalid values. It is disabled by default.

Prerequisite: [cert-manager](https://cert-manager.io/) must be installed in your cluster.

1. Install cert-manager:

    ```bash
    helm install \
      --repo https://charts.jetstack.io \
      --version v1.16.3 \
      --create-namespace \
      --namespace cert-manager \
      --wait \
      --set crds.enabled=true \
      cert-manager \
      cert-manager
    ```

2. Enable the webhook:

    ```bash
    helm install nvidia-dra-driver-gpu nvidia/nvidia-dra-driver-gpu \
        --version=v25.12.0 \
        --create-namespace \
        --namespace nvidia-dra-driver-gpu \
        --set webhook.enabled=true
    ```

To use a pre-existing TLS secret instead of cert-manager, set `webhook.tls.mode=secret` and provide `webhook.tls.secret.name` and `webhook.tls.secret.caBundle`.

---

## Validate GPU allocation

!!! note
    GPU resource allocation must be enabled at install time (`--set gpuResourcesEnabledOverride=true`). If you installed with `--set resources.gpus.enabled=false`, skip this section.

1. Confirm the kubelet plugin is running:

    ```bash
    kubectl get pod -n nvidia-dra-driver-gpu
    ```

    All `*-kubelet-plugin-*` pods should show `Ready` status.

2. Create and apply the test workload:

    ```bash
    cat <<EOF > dra-gpu-share-test.yaml
    ---
    apiVersion: v1
    kind: Namespace
    metadata:
      name: dra-gpu-share-test
    ---
    apiVersion: resource.k8s.io/v1         # Kubernetes 1.34+
    # apiVersion: resource.k8s.io/v1beta2  # Kubernetes 1.32 and 1.33
    kind: ResourceClaimTemplate
    metadata:
      namespace: dra-gpu-share-test
      name: single-gpu
    spec:
      spec:
        devices:
          requests:
          - name: gpu
            exactly:
              deviceClassName: gpu.nvidia.com
    ---
    apiVersion: v1
    kind: Pod
    metadata:
      namespace: dra-gpu-share-test
      name: pod
      labels:
        app: pod
    spec:
      containers:
      - name: ctr0
        image: ubuntu:22.04
        command: ["bash", "-c"]
        args: ["nvidia-smi -L; trap 'exit 0' TERM; sleep 9999 & wait"]
        resources:
          claims:
          - name: shared-gpu
      - name: ctr1
        image: ubuntu:22.04
        command: ["bash", "-c"]
        args: ["nvidia-smi -L; trap 'exit 0' TERM; sleep 9999 & wait"]
        resources:
          claims:
          - name: shared-gpu
      resourceClaims:
      - name: shared-gpu
        resourceClaimTemplateName: single-gpu
      tolerations:
      - key: "nvidia.com/gpu"
        operator: "Exists"
        effect: "NoSchedule"
    EOF
    kubectl apply -f dra-gpu-share-test.yaml
    ```

3. Verify both containers use the same GPU:

    ```bash
    kubectl logs pod -n dra-gpu-share-test --all-containers --prefix
    ```

    Expected output shows the same GPU UUID from both containers:

    ```
    [pod/pod/ctr0] GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-4404041a-04cf-1ccf-9e70-f139a9b1e23c)
    [pod/pod/ctr1] GPU 0: NVIDIA A100-SXM4-40GB (UUID: GPU-4404041a-04cf-1ccf-9e70-f139a9b1e23c)
    ```

4. Clean up:

    ```bash
    kubectl delete -f dra-gpu-share-test.yaml
    ```

---

## Validate ComputeDomain allocation

1. Confirm the driver is running:

    ```bash
    kubectl get pod -n nvidia-dra-driver-gpu
    ```

    Verify the controller pod and all kubelet plugin pods are `Running` and `Ready`.

2. Validate clique node labels. GPU Feature Discovery labels each MNNVL-capable node with `nvidia.com/gpu.clique`. Confirm all expected nodes have this label:

    ```bash
    (echo -e "NODE\tLABEL\tCLIQUE"; kubectl get nodes -o json | \
        jq -r '.items[] | [.metadata.name, "nvidia.com/gpu.clique", .metadata.labels["nvidia.com/gpu.clique"]] | @tsv') | \
        column -t
    ```

    Each value should have the shape `<CLUSTER_UUID>.<CLIQUE_ID>`. If any nodes are missing the label, see [Troubleshooting](troubleshooting.md#gpu-nodes-are-missing-the-nvidiacomgpuclique-label).

3. Run the IMEX channel injection test:

    ```bash
    cat <<EOF > imex-channel-injection.yaml
    ---
    apiVersion: resource.nvidia.com/v1beta1
    kind: ComputeDomain
    metadata:
      name: imex-channel-injection
    spec:
      numNodes: 1
      channel:
        resourceClaimTemplate:
          name: imex-channel-0
    ---
    apiVersion: v1
    kind: Pod
    metadata:
      name: imex-channel-injection
    spec:
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            - matchExpressions:
              - key: nvidia.com/gpu.clique
                operator: Exists
      containers:
      - name: ctr
        image: ubuntu:22.04
        command: ["bash", "-c"]
        args: ["ls -la /dev/nvidia-caps-imex-channels; trap 'exit 0' TERM; sleep 9999 & wait"]
        resources:
          claims:
          - name: imex-channel-0
      resourceClaims:
      - name: imex-channel-0
        resourceClaimTemplateName: imex-channel-0
    EOF
    kubectl apply -f imex-channel-injection.yaml
    ```

4. Verify IMEX channel injection:

    ```bash
    kubectl logs imex-channel-injection
    ```

    The output should list one or more channel device files under `/dev/nvidia-caps-imex-channels`:

    ```
    total 0
    drwxr-xr-x 2 root root  60 ...
    crw-rw-rw- 1 root root 507, 0 ... channel0
    ```

5. Clean up:

    ```bash
    kubectl delete -f imex-channel-injection.yaml
    ```

---

## Enable DRA on Kubernetes 1.32 and 1.33

On Kubernetes 1.34 and later, `DynamicResourceAllocation` is enabled by default and no additional configuration is required.

On Kubernetes 1.32 and 1.33, the feature gate must be manually enabled on four components: the API server, scheduler, controller manager, and each kubelet.

### kubeadm

1. Add `--feature-gates=DynamicResourceAllocation=true` to the `command` section of each control plane manifest:

    ```bash
    sudo vi /etc/kubernetes/manifests/kube-apiserver.yaml
    sudo vi /etc/kubernetes/manifests/kube-scheduler.yaml
    sudo vi /etc/kubernetes/manifests/kube-controller-manager.yaml
    ```

    In each file, add the flag to the existing `command` list:

    ```yaml
    spec:
      containers:
      - command:
        - kube-apiserver          # (or kube-scheduler / kube-controller-manager)
        - --feature-gates=DynamicResourceAllocation=true
        # ... other existing flags ...
    ```

2. On each node, open the kubelet configuration file:

    ```bash
    sudo vi /var/lib/kubelet/config.yaml
    ```

3. Add the feature gate:

    ```yaml
    featureGates:
      DynamicResourceAllocation: true
    ```

4. Restart the kubelet on each node:

    ```bash
    sudo systemctl restart kubelet
    ```

!!! note
    For managed Kubernetes distributions (EKS, GKE, AKS, and others), refer to your provider's documentation for enabling feature gates. Not all providers support enabling `DynamicResourceAllocation` on 1.32 or 1.33 clusters.