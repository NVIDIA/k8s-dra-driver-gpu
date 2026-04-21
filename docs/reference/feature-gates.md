# Feature gates

Feature gates control experimental and beta functionality in the DRA Driver for NVIDIA GPUs. They follow [Kubernetes feature gate conventions](https://kubernetes.io/docs/reference/command-line-tools-reference/feature-gates/).

## Set feature gates

Set feature gates in your Helm values file:

```yaml
featureGates:
  TimeSlicingSettings: true
  MPSSupport: true
```

Or pass them at install time:

```bash
helm install nvidia-dra-driver-gpu nvidia/nvidia-dra-driver-gpu \
    --set "featureGates.TimeSlicingSettings=true"
```

## Available feature gates

| Feature gate | Stage | Default | Description |
|---|---|---|---|
| `TimeSlicingSettings` | Alpha | `false` | Enables customization of CUDA time-slicing settings in `GpuConfig`. |
| `MPSSupport` | Alpha | `false` | Enables Multi-Process Service (MPS) sharing strategy in `GpuConfig` and `MigDeviceConfig`. |
| `IMEXDaemonsWithDNSNames` | Beta | `true` | IMEX daemons use DNS names instead of raw IP addresses for peer communication. Required by `ComputeDomainCliques`. |
| `PassthroughSupport` | Alpha | `false` | Enables VFIO passthrough device allocation via `VfioDeviceConfig`. |
| `DynamicMIG` | Alpha | `false` | Enables dynamic MIG device allocation and reconfiguration. |
| `NVMLDeviceHealthCheck` | Alpha | `false` | Enables GPU health checking via NVML. |
| `ComputeDomainCliques` | Beta | `true` | Uses `ComputeDomainClique` CRD objects to track IMEX daemon membership. Requires `IMEXDaemonsWithDNSNames`. |
| `CrashOnNVLinkFabricErrors` | Beta | `true` | Causes the kubelet plugin to crash rather than fall back to non-fabric mode when NVLink fabric errors are detected. |

## Constraints

The following feature gate combinations are mutually exclusive and will cause a startup error:

| Combination | Reason |
|---|---|
| `DynamicMIG` + `PassthroughSupport` | Mutually exclusive |
| `DynamicMIG` + `NVMLDeviceHealthCheck` | Mutually exclusive |
| `DynamicMIG` + `MPSSupport` | Mutually exclusive |

The following feature gates have hard dependencies:

| Feature gate | Requires |
|---|---|
| `ComputeDomainCliques` | `IMEXDaemonsWithDNSNames` |
