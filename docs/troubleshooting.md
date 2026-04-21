# Troubleshooting

## Getting help

If you've worked through this page and are still stuck, [open a Question / Support issue](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/issues/new?template=question.yml) in the repository. 



## `nvidiaDriverRoot` is set incorrectly

**Symptom**: Kubelet plugin pods start but fail to find GPU devices, or CDI injection fails.

**Cause**: The `nvidiaDriverRoot` Helm parameter must point to the root of the GPU driver installation on the host. Setting it incorrectly is the most common source of installation errors.

**Fix**: Set the correct value for your driver installation method:

| Driver installation method | Correct value |
|---|---|
| Operator-managed (driver running in a container) | `/run/nvidia/driver` |
| Host-provided (installed directly on the host OS) | `/` (default) |

Re-install or upgrade the Helm chart with the corrected value.

---

## ComputeDomain pods stay in `ContainerCreating`

**Symptom**: Workload pods referencing a `ComputeDomain` remain in `ContainerCreating` state.

**Cause**: If `nvidia-imex-*` packages are installed via the system package manager, the `nvidia-imex.service` systemd unit may be running and conflicting with the driver-managed IMEX daemons.

**Fix**: On each GPU node, disable and mask the service, then reinstall the driver:

```bash
systemctl disable --now nvidia-imex.service && systemctl mask nvidia-imex.service
```

---

## GPU nodes are missing the `nvidia.com/gpu.clique` label

**Symptom**: The clique label validation in [Validate ComputeDomain allocation](get-started.md#validate-computedomain-allocation) shows nodes with no label value.

**Cause**: The `nvidia.com/gpu.clique` label is set by the GPU Feature Discovery component. If it is not running or has not completed a discovery cycle, nodes will not have this label.

**Fix**: Verify that GPU Feature Discovery is deployed and healthy, then confirm it has completed a discovery cycle on the affected nodes. Per-GPU clique information can be inspected directly on a node with:

```bash
nvidia-smi -q | grep -E "ClusterUUID|CliqueId"
```

---

## ComputeDomain kubelet plugin crashes on NVLink fabric errors

**Symptom**: The `compute-domain-kubelet-plugin` pod crashes or restarts repeatedly. Logs contain messages about NVLink fabric errors or degraded fabric health.

**Cause**: The `CrashOnNVLinkFabricErrors` feature gate is Beta and enabled by default. When enabled, the kubelet plugin deliberately crashes instead of falling back to a degraded mode when NVLink fabric errors are detected. This is the intended behavior — it surfaces fabric problems loudly rather than allowing workloads to run on a degraded fabric.

**Fix**: Investigate and resolve the underlying NVLink fabric issue. If you need to temporarily disable the crash behavior (for example, while diagnosing the root cause), disable the gate:

```yaml
featureGates:
  CrashOnNVLinkFabricErrors: false
```

---

## Controlling log verbosity

To increase log output for debugging, set `logVerbosity` when upgrading:

```bash
helm upgrade -i nvidia-dra-driver-gpu nvidia/nvidia-dra-driver-gpu \
    --namespace nvidia-dra-driver-gpu \
    --set logVerbosity=6
```

See the [Get started](get-started.md#configure) guide for a description of verbosity levels and what each level produces.


