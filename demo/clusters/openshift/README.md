# Running the NVIDIA DRA Driver on Red Hat OpenShift

This document explains the differences between deploying the NVIDIA DRA driver on OpenShift and upstream Kubernetes or its flavors.

## Prerequisites

Install OpenShift 4.16 or later. You can use the Assisted Installer to install on bare metal, or obtain an IPI installer binary (`openshift-install`) from the [OpenShift clients page](https://mirror.openshift.com/pub/openshift-v4/clients/ocp/) page. Refer to the [OpenShift documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/latest/html/installation_overview/ocp-installation-overview) for different installation methods.

## Enabling DRA on OpenShift

Enable the `TechPreviewNoUpgrade` feature set as explained in [Enabling features using FeatureGates](https://docs.redhat.com/en/documentation/openshift_container_platform/latest/html/nodes/working-with-clusters#nodes-cluster-enabling-features-about_nodes-cluster-enabling), either during the installation or post-install. The feature set includes the `DynamicResourceAllocation` feature gate.

Update the cluster scheduler to enable the DRA scheduling plugin:

```console
$ oc patch --type merge -p '{"spec":{"profile": "HighNodeUtilization", "profileCustomizations": {"dynamicResourceAllocation": "Enabled"}}}' scheduler cluster
```

## NVIDIA GPU Drivers

The easiest way to install NVIDIA GPU drivers on OpenShift nodes is via the NVIDIA GPU Operator with the device plugin disabled. Follow the installation steps in [NVIDIA GPU Operator on Red Hat OpenShift Container Platform](https://docs.nvidia.com/datacenter/cloud-native/openshift/latest/index.html), and **_be careful to disable the device plugin so it does not conflict with the DRA plugin_**:

```yaml
  devicePlugin:
    enabled: false
```

## NVIDIA Binaries on RHCOS

The location of some NVIDIA binaries on an OpenShift node differs from the defaults. Make sure to pass the following values when installing the Helm chart:

```yaml
nvidiaDriverRoot: /run/nvidia/driver
nvidiaCtkPath: /var/usrlocal/nvidia/toolkit/nvidia-ctk
```

## OpenShift Security

OpenShift generally requires more stringent security settings than Kubernetes. If you see a warning about security context constraints when deploying the DRA plugin, pass the following to the Helm chart, either via an in-line variable or a values file:

```yaml
kubeletPlugin:
  containers:
    plugin:
      securityContext:
        privileged: true
        seccompProfile:
          type: Unconfined
```

If you see security context constraints errors/warnings when deploying a sample workload, make sure to update the workload's security settings according to the [OpenShift documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/latest/html/operators/developing-operators#osdk-complying-with-psa). Usually applying the following `securityContext` definition at a pod or container level works for non-privileged workloads.

```yaml
  securityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
    allowPrivilegeEscalation: false
    capabilities:
      drop:
        - ALL
```

## Using Multi-Instance GPU (MIG)

Workloads that use the Multi-instance GPU (MIG) feature require MIG to be enabled on the worker nodes with [MIG-supported GPUs](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/index.html#supported-gpus), e.g. A100.

First, make sure to stop any custom pods that might be using the GPU to avoid disruption when the new MIG configuration is applied.

Disable the CUDA validator so that it does not try to find a MIG partition (or a GPU) to run on, because none will be available until dynamically created by the DRA driver.

```yaml
validator:
  <...>
  cuda:
    env:
      - name: WITH_WORKLOAD
        value: 'false'
  <...>
```

Enable MIG via the MIG manager of the NVIDIA GPU Operator. **Do not configure MIG devices as the DRA driver will do it automatically on the fly**:

```console
$ oc label node <node> nvidia.com/mig.config=all-enabled --overwrite
```

MIG will be automatically enabled on the labeled nodes, and all existing MIG partitions will be deleted. For additional information, see [MIG Support in OpenShift Container Platform](https://docs.nvidia.com/datacenter/cloud-native/openshift/latest/mig-ocp.html).

**Note:**
The `all-enabled` MIG configuration profile is available out of the box in the NVIDIA GPU Operator starting v24.3. With an earlier version, you may need to [create a custom profile](https://docs.nvidia.com/datacenter/cloud-native/openshift/latest/mig-ocp.html#creating-and-applying-a-custom-mig-configuration).

Update the cluster policy to include:

```yaml
  migManager:
    ...
    env:
      - name: MIG_PARTED_MODE_CHANGE_ONLY
        value: 'true'
    ...
```

Setting `MIG_PARTED_MODE_CHANGE_ONLY=true` will prevent the MIG Manager from interfering with the DRA driver.

You can verify the MIG status using the `nvidia-smi` command from a GPU driver pod:

```console
$ oc exec -ti nvidia-driver-daemonset-<suffix> -n nvidia-gpu-operator -- nvidia-smi
+-----------------------------------------------------------------------------------------+
| NVIDIA-SMI 550.54.14              Driver Version: 550.54.14      CUDA Version: N/A      |
|-----------------------------------------+------------------------+----------------------+
| GPU  Name                 Persistence-M | Bus-Id          Disp.A | Volatile Uncorr. ECC |
| Fan  Temp   Perf          Pwr:Usage/Cap |           Memory-Usage | GPU-Util  Compute M. |
|                                         |                        |               MIG M. |
|=========================================+========================+======================|
|   0  NVIDIA A100 80GB PCIe          On  |   00000000:17:00.0 Off |                   On |
| N/A   35C    P0             45W /  300W |       0MiB /  81920MiB |     N/A      Default |
|                                         |                        |              Enabled |
+-----------------------------------------+------------------------+----------------------+
```

**Note:**
On some cloud service providers (CSP), the CSP blocks GPU reset for GPUs passed into a VM. In this case [ensure](https://docs.nvidia.com/datacenter/cloud-native/gpu-operator/latest/gpu-operator-mig.html#enabling-mig-during-installation) that the `WITH_REBOOT` environment variable is set to `true`:

```yaml
  migManager:
    ...
    env:
      - name: WITH_REBOOT
        value: 'true'
    ...
```

When MIG settings could not be fully applied, the MIG status will be marked with an asterisk (i.e. `Enabled*`) and you will need to reboot the nodes manually.

See the [NVIDIA Multi-Instance GPU User Guide](https://docs.nvidia.com/datacenter/tesla/mig-user-guide/index.html) for more information about MIG.
