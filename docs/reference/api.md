# API reference

This page documents the opaque configuration types that you place in `ResourceClaim` and `ResourceClaimTemplate` specs to configure GPU and ComputeDomain resources.

All types use API version `resource.nvidia.com/v1beta1`.

---

## GpuConfig

Configures a GPU device. Set as the opaque config in a `ResourceClaim` targeting `gpu.nvidia.com`.

With time-slicing:

```yaml
apiVersion: resource.nvidia.com/v1beta1
kind: GpuConfig
sharing:
  strategy: TimeSlicing
  timeSlicingConfig:
    interval: Default       # Default | Short | Medium | Long
```

With MPS (Multi-Process Service):

```yaml
apiVersion: resource.nvidia.com/v1beta1
kind: GpuConfig
sharing:
  strategy: MPS
  mpsConfig:
    defaultActiveThreadPercentage: 50             # optional, integer
    defaultPinnedDeviceMemoryLimit: "4Gi"         # optional, quantity
    defaultPerDevicePinnedMemoryLimit:            # optional, map
      "0": "2Gi"
```

### Fields

| Field | Type | Description |
|---|---|---|
| `sharing` | object | Optional. Sharing strategy and its configuration. Omit to use the device exclusively. |
| `sharing.strategy` | string | `TimeSlicing` or `MPS` (Multi-Process Service). |
| `sharing.timeSlicingConfig.interval` | string | Time-slice duration: `Default`, `Short`, `Medium`, or `Long`. Requires the `TimeSlicingSettings` feature gate. |
| `sharing.mpsConfig.defaultActiveThreadPercentage` | integer | Thread percentage limit applied to all processes sharing the GPU. Requires the `MPSSupport` feature gate. |
| `sharing.mpsConfig.defaultPinnedDeviceMemoryLimit` | quantity | Pinned memory limit applied to all devices. |
| `sharing.mpsConfig.defaultPerDevicePinnedMemoryLimit` | map | Per-device override of `defaultPinnedDeviceMemoryLimit`. Keys are device index (integer) or UUID string. |

---

## MigDeviceConfig

Configures a Multi-Instance GPU (MIG) device slice. Set as the opaque config in a `ResourceClaim` targeting a MIG DeviceClass.

With time-slicing:

```yaml
apiVersion: resource.nvidia.com/v1beta1
kind: MigDeviceConfig
sharing:
  strategy: TimeSlicing
```

With MPS:

```yaml
apiVersion: resource.nvidia.com/v1beta1
kind: MigDeviceConfig
sharing:
  strategy: MPS
  mpsConfig:
    defaultActiveThreadPercentage: 50
```

### Fields

| Field | Type | Description |
|---|---|---|
| `sharing` | object | Optional. Supports `TimeSlicing` and `MPS` strategies. |
| `sharing.strategy` | string | `TimeSlicing` or `MPS`. |
| `sharing.mpsConfig` | object | MPS configuration. Same fields as `GpuConfig.sharing.mpsConfig`. Requires the `MPSSupport` feature gate. |

> **Note**: `timeSlicingConfig` is not available for MIG devices. Time-slicing on MIG slices does not support interval configuration.

---

## VfioDeviceConfig

Configures a Virtual Function I/O (VFIO) passthrough device. No additional fields beyond the type metadata.

```yaml
apiVersion: resource.nvidia.com/v1beta1
kind: VfioDeviceConfig
```

> Requires the `PassthroughSupport` feature gate. See [Feature gates](feature-gates.md).

---

## ComputeDomain

A `ComputeDomain` is a Custom Resource Definition (CRD) that provisions an ephemeral multi-node NVLink fabric. Create one per workload; the controller manages the underlying IMEX daemon lifecycle automatically.

```yaml
apiVersion: resource.nvidia.com/v1beta1
kind: ComputeDomain
metadata:
  name: my-compute-domain
spec:
  numNodes: 0
  channel:
    resourceClaimTemplate:
      name: my-channel
    allocationMode: Single        # Single (default) | All
```

### Spec fields

| Field | Type | Default | Description |
|---|---|---|---|
| `spec.numNodes` | integer | — | Deprecated. Formerly used to gate workload startup until all IMEX daemons joined. Set to `0` when using the default `IMEXDaemonsWithDNSNames` feature gate. |
| `spec.channel.resourceClaimTemplate.name` | string | — | Name of the `ResourceClaimTemplate` the controller creates for workload pods to claim a channel from. |
| `spec.channel.allocationMode` | string | `Single` | `Single` allocates one IMEX channel per claim. `All` allocates the maximum number of channels available in the IMEX domain. |

### Status fields

| Field | Type | Description |
|---|---|---|
| `status.status` | string | `Ready` when all expected IMEX daemons have joined; `NotReady` otherwise. |
| `status.nodes[].name` | string | Node name. |
| `status.nodes[].ipAddress` | string | Node IP used by the IMEX daemon. |
| `status.nodes[].cliqueID` | string | NVLink clique identifier for the node. |
| `status.nodes[].status` | string | Per-node daemon readiness: `Ready` or `NotReady`. |

> **Note**: Do not gate workload startup on `status.status`. The status field is informational. IMEX daemons start as soon as their local node joins without waiting for all peers.
