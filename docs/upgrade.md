# Upgrade from v25.12.0 to v0.4.0

Upgrade the DRA Driver for NVIDIA GPUs from `v25.12.0` to `v0.4.0` without disrupting existing workloads. For the full release summary, see the [v0.4.0 release notes](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/releases/tag/v0.4.0).


---

## What changed

The v0.4.0 release introduces several changes that affect how this upgrade is performed:

- The project moved from `NVIDIA/k8s-dra-driver-gpu` to `kubernetes-sigs/dra-driver-nvidia-gpu`. The Go module is now `sigs.k8s.io/nvidia-dra-driver-gpu` and container images are published to `registry.k8s.io/dra-driver-nvidia/dra-driver-nvidia-gpu` in addition to NVIDIA NGC Catalog (NGC).
- The Helm chart name changed from `nvidia-dra-driver-gpu` to `dra-driver-nvidia-gpu`. To keep existing Kubernetes resource names (DaemonSets, Deployments, ServiceAccounts, RBAC) stable, `--set nameOverride=nvidia-dra-driver-gpu` is required on the first upgrade. See [Upgrade procedure](#upgrade-procedure) below.
- In addition to NGC (`nvidia/dra-driver-nvidia-gpu`), the DRA Driver Helm chart is now also published to `oci://registry.k8s.io/dra-driver-nvidia/charts/dra-driver-nvidia-gpu`. You can continue to use the NGC chart or switch to the Kubernetes registry.
- Starting in v0.4.0, the chart follows SemVer and `--version 0.4.0` is required on `helm install` and `helm upgrade`.
- Once the cluster is on v0.4.0, downgrading to v25.12.0 is not supported. Two changes prevent downgrade: the kubelet plugin checkpoint format added a `BootID` field, and the `ComputeDomain` API now allows `numNodes` to be omitted. Plan this upgrade as forward-only. See the [v0.4.0 release notes](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/releases/tag/v0.4.0) for more details.

## Before you begin

- Collect the `--set` flags you used at install time (for example, `gpuResourcesEnabledOverride`, `nvidiaDriverRoot`, `webhook.enabled`). You will pass the same flags on `helm upgrade`.
- If any node hit the "device cannot be reprepared after host reboot" issue ([#951](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/issues/951)) prior to v0.4.0, remove the kubelet plugin checkpoint file on that node before upgrading. The new BootID-aware checkpoint format in v0.4.0 only invalidates checkpoints that already carry a recorded BootID; legacy checkpoints written by v25.12.0 are otherwise assumed valid.

## Upgrade procedure

Perform the following steps in order.

1. Update custom resource definitions.

Apply the v0.4.0 CRDs before upgrading the Helm chart. Helm only installs CRDs on first install and does not update them on `helm upgrade`, so applying them explicitly ensures the API schema is ready before the new controller and kubelet plugin start.

Update the ComputeDomains CRD:

```bash
kubectl apply \
    -f https://raw.githubusercontent.com/kubernetes-sigs/dra-driver-nvidia-gpu/refs/tags/v0.4.0/deployments/helm/dra-driver-nvidia-gpu/crds/resource.nvidia.com_computedomains.yaml
```

Update the ComputeDomainsCliques CRD:

```bash
kubectl apply \
    -f https://raw.githubusercontent.com/kubernetes-sigs/dra-driver-nvidia-gpu/refs/tags/v0.4.0/deployments/helm/dra-driver-nvidia-gpu/crds/resource.nvidia.com_computedomaincliques.yaml
```

2. Upgrade the Helm chart by using the `helm upgrade -i` command to upgrade the chart in place. 

Two flags are required to upgrade to v0.4.0:

- `--version 0.4.0`, because v0.4.0 introduces SemVer.
- `--set nameOverride=nvidia-dra-driver-gpu`, because the chart was renamed. Without this override, the new chart creates duplicate Kubernetes objects (kubelet plugin DaemonSet, controller Deployment, RBAC, and so on) under the new name instead of upgrading the existing ones. This is only required on the first upgrade to v0.4.0 or later.

The following command upgrades the chart and switches to using the Kubernetes registry source:

```bash
helm upgrade -i nvidia-dra-driver-gpu oci://registry.k8s.io/dra-driver-nvidia/charts/dra-driver-nvidia-gpu \
    --version 0.4.0 \
    --namespace nvidia-dra-driver-gpu \
    --set gpuResourcesEnabledOverride=true \
    --set nameOverride=nvidia-dra-driver-gpu
```

Append any additional `--set` flags you used at install time. For example, `--set nvidiaDriverRoot=/run/nvidia/driver` if the NVIDIA GPU Operator manages your drivers.

Example output:

```
Release "nvidia-dra-driver-gpu" has been upgraded. Happy Helming!
NAME: nvidia-dra-driver-gpu
LAST DEPLOYED: Wed May 13 05:30:39 2026
NAMESPACE: nvidia-dra-driver-gpu
STATUS: deployed
REVISION: 2
TEST SUITE: None
```

> Note: Subsequent `helm upgrade` calls do not need `--set nameOverride=nvidia-dra-driver-gpu`. It is only required on the first upgrade to v0.4.0 or later.

## Verify the upgrade

After `helm upgrade` completes, confirm the new pods are running and pre-existing workloads are still healthy.

1. Check that all driver pods are `Running` and `Ready`:

```bash
kubectl get pods -n nvidia-dra-driver-gpu
```

Example output:

```
NAME                                                READY   STATUS    RESTARTS   AGE
nvidia-dra-driver-gpu-controller-5c968c745f-s8n2m   1/1     Running   0          13s
nvidia-dra-driver-gpu-kubelet-plugin-6fmmd          2/2     Running   0          112s
```

The `controller` pod runs the ComputeDomain controller. The `kubelet-plugin` pod runs two containers (`gpus` and `compute-domains`) when both resource plugins are enabled, so it reports `2/2`.

2. Confirm every pre-existing `ResourceClaim` is still allocated and reserved for its pod:

```bash
kubectl get resourceclaims -A
```

Example output, showing a ComputeDomain workload and two GPU workloads that were running before the upgrade:

```
NAMESPACE               NAME                                                           STATE                AGE
default                 imex-channel-injection-imex-channel-0-c5pnt                    allocated,reserved   13s
nvidia-dra-driver-gpu   computedomain-daemon-dc84d905-2336-45fa-9-compute-domaifb5sg   allocated,reserved   12s
gpu-test1               pod1-gpu-7r7zn                                                 allocated,reserved   119s
gpu-test1               pod2-gpu-stmv8                                                 allocated,reserved   119s
```

Every claim that was bound before the upgrade should still report `allocated,reserved`.

## Troubleshooting

If a workload pod is in a non-`Running` state after the upgrade, capture the kubelet plugin logs from the node where the pod was scheduled:

```bash
kubectl logs -n nvidia-dra-driver-gpu <kubelet-plugin-pod>
```

If you see `checkpoint is corrupted` errors, the v0.4.0 kubelet plugin now logs a diff between the on-disk and re-marshaled checkpoint contents to make this easier to debug. Include that log output when filing an [issue](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/issues).
