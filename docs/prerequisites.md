# Prerequisites

Cluster, software, and hardware requirements for the DRA Driver for NVIDIA GPUs.

> Tip: Most of these prerequisites can be installed and managed for you by the [NVIDIA GPU Operator](#install-prerequisites-with-nvidia-gpu-operator).


| Requirement | Version / Notes |
|---|---|
| Kubernetes | v1.34.2 or later, with at least one node that has one or more NVIDIA GPUs. |
| `DynamicResourceAllocation` feature gate | Enabled by default in Kubernetes v1.34+. On v1.32 and v1.33, [enable it manually](#enable-dra-on-kubernetes-v132-and-v133). |
| Helm | v3.8 or later. |
| NVIDIA Driver | v565 or later for GPU allocation. v570.158.01 or later if using [ComputeDomains](#computedomains-additional-prerequisites). |
| CDI  | Enabled in your container runtime. This is enabled by default in containerd 2.0+ and CRIO v1.27+. The DRA Driver uses CDI to expose GPUs to containers.  |
| Node Feature Discovery (NFD) | Labels GPU nodes in the cluster. The DRA Driver uses these labels to target the GPU kubelet plugin to the correct nodes. |

## ComputeDomains additional prerequisites

If you plan to use ComputeDomains, you also need:

- NVIDIA Driver v570.158.01 or later. The `IMEXDaemonsWithDNSNames` feature gate is enabled by default and requires this driver version. The ComputeDomain plugin will fail to start on older drivers unless `IMEXDaemonsWithDNSNames` is explicitly disabled.
- Multi-Node NVLink (MNNVL) hardware. Nodes must be connected via NVLink fabric, such as GB200 NVL72 and similar systems.
- GPU Feature Discovery (GFD) deployed via the [GPU Operator](#install-prerequisites-with-nvidia-gpu-operator). GFD generates the `nvidia.com/gpu.clique` node labels required by ComputeDomains.
- On all GPU nodes where the `nvidia-imex-*` packages are installed, the `nvidia-imex.service` systemd unit must be disabled:

```bash
systemctl disable --now nvidia-imex.service && systemctl mask nvidia-imex.service
```

## Install prerequisites with NVIDIA GPU Operator

The [NVIDIA GPU Operator](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/index.html) is a Kubernetes operator that automates the deployment and lifecycle management of all NVIDIA software components needed to provision and monitor GPUs in a cluster.

It can manage the following DRA Driver for NVIDIA GPUs prerequisites for you:

- NVIDIA Driver (v565+ for GPU allocation, v570.158.01+ for ComputeDomains). The GPU Operator installs a [default driver](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/platform-support.html#gpu-operator-component-matrix) that meets the DRA Driver's prerequisites. To use a specific version, see [Common chart customization options](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/getting-started.html#common-chart-customization-options) in the GPU Operator documentation.
- CDI enabled through the NVIDIA Container Toolkit.
- Node Feature Discovery (NFD).
- GPU Feature Discovery (GFD), required for ComputeDomains.

If you choose to install the GPU Operator, follow the [DRA Driver for NVIDIA GPUs install guide](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/dra-intro-install.html) in the GPU Operator documentation. It covers installing the GPU Operator with the NVIDIA Kubernetes Device Plugin disabled and installing the DRA Driver for NVIDIA GPUs.

## Enable DRA on Kubernetes v1.32 and v1.33

On Kubernetes v1.34 and later, `DynamicResourceAllocation` is enabled by default and no additional configuration is required.

On Kubernetes v1.32 and v1.33, enable the following on each component:

| Component | Requirement |
|---|---|
| kube-apiserver | Enable the `DynamicResourceAllocation` feature gate and the `resource.k8s.io/v1beta1` API group (available on v1.32 and v1.33). On v1.33, also enable `resource.k8s.io/v1beta2`. |
| kube-controller-manager | Enable the `DynamicResourceAllocation` feature gate |
| kube-scheduler | Enable the `DynamicResourceAllocation` feature gate |
| kubelet | Enable the `DynamicResourceAllocation` feature gate |

How you apply these depends on your cluster setup. For managed Kubernetes distributions (EKS, GKE, AKS, and others), refer to your provider's documentation. Not all providers support enabling `DynamicResourceAllocation` on v1.32 or v1.33 clusters.

### Example: kubeadm

The following `kubeadm-init.yaml` enables DRA for a new cluster using [kubeadm](https://kubernetes.io/docs/setup/production-environment/tools/kubeadm/control-plane-flags/):

```yaml
apiVersion: kubeadm.k8s.io/v1beta4
kind: ClusterConfiguration
apiServer:
  extraArgs:
  - name: "feature-gates"
    value: "DynamicResourceAllocation=true"
  - name: "runtime-config"
    # On v1.32, omit "resource.k8s.io/v1beta2=true" 
    value: "resource.k8s.io/v1beta1=true,resource.k8s.io/v1beta2=true"
controllerManager:
  extraArgs:
  - name: "feature-gates"
    value: "DynamicResourceAllocation=true"
scheduler:
  extraArgs:
  - name: "feature-gates"
    value: "DynamicResourceAllocation=true"
---
apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
featureGates:
  DynamicResourceAllocation: true
```
