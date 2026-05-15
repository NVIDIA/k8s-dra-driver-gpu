# Architecture

This repo ships two independent Kubernetes DRA drivers from one codebase:

- **`gpu.nvidia.com`** — allocates GPUs, MIG slices, and VFIO passthrough.
- **`compute-domain.nvidia.com`** — allocates IMEX daemons and channels for Multi-Node NVLink.

Both are delivered by the Helm chart in [`helm/dra-driver-nvidia-gpu/`](../helm/dra-driver-nvidia-gpu).

## Binaries ([`cmd/`](../cmd))

| Binary | Runs as | What it does |
|---|---|---|
| `gpu-kubelet-plugin` | DaemonSet, per node | Publishes GPU / MIG / VFIO `ResourceSlice`s and injects CDI on Prepare. |
| `compute-domain-kubelet-plugin` | Same DaemonSet, per node | Publishes IMEX daemon + channel devices and injects the IMEX mount on Prepare. |
| `compute-domain-controller` | Cluster Deployment | Watches `ComputeDomain` CRs; spawns a per-CD DaemonSet and the matching `ResourceClaimTemplate`s. |
| `compute-domain-daemon` | Per-CD DaemonSet, per node | Wraps and supervises `nvidia-imex`; reports peers. |
| `webhook` | Cluster Deployment | Validates opaque config on `ResourceClaim`s. |

## GPU request

Pod → `ResourceClaim` with a `GpuConfig` / `MigDeviceConfig` / `VfioDeviceConfig` → webhook validates → scheduler binds a device advertised by the GPU plugin → kubelet calls Prepare → plugin writes a CDI spec → runtime injects the GPU into the container.

## ComputeDomain

User creates a `ComputeDomain` → controller creates a per-CD DaemonSet → each daemon pod runs `nvidia-imex` → daemons publish their IP and clique through `ComputeDomainClique` CRs → workload pods claim a channel from the `compute-domain-default-channel.nvidia.com` DeviceClass → the CD kubelet plugin asserts readiness and injects `/dev/nvidia-caps-imex-channels/chan*` plus `/imexd` into the container.

## Reference

- CRD and opaque-config types: [`api/nvidia.com/resource/v1beta1`](../api/nvidia.com/resource/v1beta1).
- Feature gates: [`pkg/featuregates/featuregates.go`](../pkg/featuregates/featuregates.go).
- DeviceClasses shipped by the chart: [`helm/dra-driver-nvidia-gpu/templates/deviceclass-*.yaml`](../helm/dra-driver-nvidia-gpu/templates).
- Example workloads: [`demo/specs/quickstart/`](../demo/specs/quickstart).
