# Configure MIG

MIG (Multi-Instance GPU) partitions a supported GPU into hardware-isolated slices. Each slice has its own dedicated compute engines, memory bandwidth, and L2 cache. MIG is supported on H100, A100, and newer NVIDIA GPU architectures.

Use the `mig.nvidia.com` DeviceClass to request MIG slices. See [GPU Allocation](gpu-allocation.md#mig-slices) for a comparison with other device types.

---

## Prerequisites

MIG must be enabled on each GPU node before the driver can discover MIG devices. Enable MIG mode using `nvidia-smi`:

```bash
sudo nvidia-smi -mig 1
```

After enabling MIG mode, configure the desired MIG profiles on the GPU. The profiles available depend on the GPU model. To list supported profiles:

```bash
nvidia-smi mig -lgip
```

The driver discovers the configured MIG profiles at startup and advertises each as a separate device in the node's `ResourceSlice`.

---

## Request any MIG slice

To request any available MIG slice on the cluster without specifying a size:

```yaml
apiVersion: resource.k8s.io/v1         # Kubernetes 1.34+
# apiVersion: resource.k8s.io/v1beta2  # Kubernetes 1.32 and 1.33
kind: ResourceClaimTemplate
metadata:
  name: any-mig
spec:
  spec:
    devices:
      requests:
      - name: mig
        exactly:
          deviceClassName: mig.nvidia.com
```

---

## Request a MIG slice by capacity

MIG devices publish `memory` and `multiprocessors` as capacities. Use CEL selectors to filter by minimum requirements:

```yaml
apiVersion: resource.k8s.io/v1         # Kubernetes 1.34+
# apiVersion: resource.k8s.io/v1beta2  # Kubernetes 1.32 and 1.33
kind: ResourceClaimTemplate
metadata:
  name: mig-10gb
spec:
  spec:
    devices:
      requests:
      - name: mig
        exactly:
          deviceClassName: mig.nvidia.com
          selectors:
          - cel:
              expression: |
                device.capacity['gpu.nvidia.com'].memory.isGreaterThan(quantity("10Gi"))
```

To also filter by compute:

```yaml
expression: |
  device.capacity['gpu.nvidia.com'].multiprocessors.isGreaterThan(quantity("10"))
  && device.capacity['gpu.nvidia.com'].memory.isGreaterThan(quantity("10Gi"))
```

### MIG device capacities

| Capacity | Description |
|---|---|
| `memory` | Dedicated memory allocated to this slice |
| `multiprocessors` | Streaming multiprocessor count |
| `copyEngines` | Copy engine count |
| `decoders` | Decoder engine count |
| `encoders` | Encoder engine count |
| `jpegEngines` | JPEG engine count |
| `ofaEngines` | Optical flow accelerator count |

---

## Request a MIG slice by profile name

Each MIG device also publishes a `profile` attribute containing the profile name string (for example, `1g.5gb` or `3g.20gb`). Use this to pin to a specific profile:

```yaml
selectors:
- cel:
    expression: |
      device.attributes['gpu.nvidia.com'].profile == "1g.5gb"
```

!!! note
    Profile names are GPU model-specific. Pinning to a profile name makes the claim unschedulable on clusters with different GPU models. Prefer capacity-based selectors for portability.

---

## Use the claim in a pod

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: mig-workload
spec:
  containers:
  - name: app
    image: my-image
    resources:
      claims:
      - name: mig
  resourceClaims:
  - name: mig
    resourceClaimTemplateName: mig-10gb
```

---

## Configure sharing on a MIG slice

MIG slices support time-slicing and MPS. Add a `MigDeviceConfig` block to your `ResourceClaimTemplate`.

Requires the `TimeSlicingSettings` (for time-slicing) or `MPSSupport` (for MPS) Alpha feature gate. See [Configure GPU Sharing](configure-sharing.md) for how to enable these gates.

**Time-slicing on a MIG slice:**

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

**MPS on a MIG slice:**

```yaml
config:
- requests: ["mig"]
  opaque:
    driver: gpu.nvidia.com
    parameters:
      apiVersion: resource.nvidia.com/v1beta1
      kind: MigDeviceConfig
      sharing:
        strategy: MPS
        mpsConfig:
          defaultActiveThreadPercentage: 50
          defaultPinnedDeviceMemoryLimit: 5Gi
```

---

## Dynamic MIG (Alpha)

The `DynamicMIG` feature gate (Alpha, default: `false`) enables the driver to create and destroy MIG profiles on demand instead of requiring them to be preconfigured on the node. When enabled, the driver advertises the GPU's partitioning capacity rather than static MIG devices.

!!! warning "Known limitations in v25.12"
    - **A100 is not supported.** Dynamic MIG requires H100 or newer GPUs.
    - **Kubernetes v1.34+ recommended**, with the [`DRAPartitionableDevices`](https://github.com/kubernetes/enhancements/tree/master/keps/sig-scheduling/4815-dra-partitionable-devices) feature gate enabled on the API server, scheduler, and kubelet.
    - When `DynamicMIG` is enabled, the driver **takes full ownership of MIG configuration** on the node at startup. It will tear down any MIG devices it does not recognize. Disable any other tool that creates or manages MIG devices on the same node.
    - Mutually exclusive with `PassthroughSupport`, `NVMLDeviceHealthCheck`, and `MPSSupport`. See [Feature gates](reference/feature-gates.md).
