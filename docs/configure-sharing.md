# Configure GPU sharing

By default, each `ResourceClaim` gets exclusive access to a GPU. GPU sharing lets multiple containers share a single GPU by configuring a sharing strategy on the claim.

Two strategies are available: **time-slicing** and **MPS (Multi-Process Service)**.

!!! note "Alpha features"
    Both sharing strategies require Alpha feature gates that are disabled by default. Enable them at install time or in your Helm values before using the examples on this page.

---

## Enable the feature gates

Enable the feature gates you need:

```bash
helm upgrade nvidia-dra-driver-gpu nvidia/nvidia-dra-driver-gpu \
    --reuse-values \
    --set "featureGates.TimeSlicingSettings=true" \
    --set "featureGates.MPSSupport=true"
```

Or in a values file:

```yaml
featureGates:
  TimeSlicingSettings: true
  MPSSupport: true
```

Refer to [Feature gates](reference/feature-gates.md) for more detail.

---

## Time-slicing

Time-slicing uses CUDA preemption to schedule multiple containers on the same GPU. Each container gets a time slot; there is no memory isolation between containers.

Add a `GpuConfig` block with `strategy: TimeSlicing` to your `ResourceClaimTemplate`:

```yaml
apiVersion: resource.k8s.io/v1         # Kubernetes 1.34+
# apiVersion: resource.k8s.io/v1beta2  # Kubernetes 1.32 and 1.33
kind: ResourceClaimTemplate
metadata:
  name: timesliced-gpu
spec:
  spec:
    devices:
      requests:
      - name: gpu
        exactly:
          deviceClassName: gpu.nvidia.com
      config:
      - requests: ["gpu"]
        opaque:
          driver: gpu.nvidia.com
          parameters:
            apiVersion: resource.nvidia.com/v1beta1
            kind: GpuConfig
            sharing:
              strategy: TimeSlicing
              timeSlicingConfig:
                interval: Short
```

Multiple containers in the same pod can reference the same claim:

```yaml
spec:
  containers:
  - name: app-a
    resources:
      claims:
      - name: gpu
  - name: app-b
    resources:
      claims:
      - name: gpu
  resourceClaims:
  - name: gpu
    resourceClaimTemplateName: timesliced-gpu
```

### Time-slice intervals

The `interval` field controls the CUDA preemption granularity:

| Value | Description |
|---|---|
| `Default` | Platform default. Equivalent to not setting time-slicing. |
| `Short` | Shorter time slices — lower latency, more context-switch overhead |
| `Medium` | Balanced |
| `Long` | Longer time slices — higher throughput for compute-bound workloads |

---

## MPS (Multi-Process Service)

MPS runs an NVIDIA MPS control daemon that allows multiple CUDA processes to share the GPU concurrently. Unlike time-slicing, MPS supports configurable thread percentage and pinned memory limits per container.

Add a `GpuConfig` block with `strategy: MPS`:

```yaml
apiVersion: resource.k8s.io/v1         # Kubernetes 1.34+
# apiVersion: resource.k8s.io/v1beta2  # Kubernetes 1.32 and 1.33
kind: ResourceClaimTemplate
metadata:
  name: mps-gpu
spec:
  spec:
    devices:
      requests:
      - name: gpu
        exactly:
          deviceClassName: gpu.nvidia.com
      config:
      - requests: ["gpu"]
        opaque:
          driver: gpu.nvidia.com
          parameters:
            apiVersion: resource.nvidia.com/v1beta1
            kind: GpuConfig
            sharing:
              strategy: MPS
              mpsConfig:
                defaultActiveThreadPercentage: 50
                defaultPinnedDeviceMemoryLimit: 10Gi
```

### MPS configuration fields

All fields are optional. Omitting `mpsConfig` entirely uses MPS with no thread or memory limits.

| Field | Type | Description |
|---|---|---|
| `defaultActiveThreadPercentage` | integer | Maximum percentage of GPU compute threads available to each MPS client (1–100). |
| `defaultPinnedDeviceMemoryLimit` | quantity | Pinned memory limit applied to all MPS clients sharing this GPU. |
| `defaultPerDevicePinnedMemoryLimit` | map | Pinned memory limit per device, keyed by device index or UUID. Overrides `defaultPinnedDeviceMemoryLimit` for the specified device. |

---

## Sharing on MIG slices

MIG slices support both time-slicing and MPS sharing using a `MigDeviceConfig` config block instead of `GpuConfig`. The example below shows time-slicing:

```yaml
apiVersion: resource.k8s.io/v1         # Kubernetes 1.34+
# apiVersion: resource.k8s.io/v1beta2  # Kubernetes 1.32 and 1.33
kind: ResourceClaimTemplate
metadata:
  name: mig-timesliced
spec:
  spec:
    devices:
      requests:
      - name: mig
        exactly:
          deviceClassName: mig.nvidia.com
      config:
      - requests: ["mig"]
        opaque:
          driver: gpu.nvidia.com
          parameters:
            apiVersion: resource.nvidia.com/v1beta1
            kind: MigDeviceConfig
            sharing:
              strategy: TimeSlicing
```

MIG slices do support MPS sharing. Use `strategy: MPS` with a `MigDeviceConfig` (the `mpsConfig` fields are available, but `timeSlicingConfig` is not valid on MIG slices).

---

## Choosing a strategy

| | Time-slicing | MPS |
|---|---|---|
| Memory isolation | No | Yes (configurable pinned limits) |
| Compute isolation | No | Yes (configurable thread %) |
| Works on MIG slices | Yes | Yes |
| Feature gate | `TimeSlicingSettings` | `MPSSupport` |
| Best for | Low-priority or bursty workloads | Inference serving, multi-tenant GPU pods |