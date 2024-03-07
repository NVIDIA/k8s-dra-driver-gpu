# Running the NVIDIA DRA Driver on Red Hat OpenShift

This document covers OpenShift-specific requirements for deploying the NVIDIA DRA Driver.

## OpenShift Version

Ensure you have OpenShift 4.19 or later. For installation methods, see [OpenShift documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/latest/html/installation_overview/ocp-installation-overview).

## `TechPreviewNoUpgrade` Feature Set

On OpenShift 4.20 (Kubernetes 1.33) and earlier, enable DRA through the `TechPreviewNoUpgrade` feature set (includes the `DynamicResourceAllocation` feature gate), either during installation or post-install. See [Enabling features using FeatureGates](https://docs.redhat.com/en/documentation/openshift_container_platform/latest/html/nodes/working-with-clusters#nodes-cluster-enabling-features-about_nodes-cluster-enabling).

**WARNING:** Enabling `TechPreviewNoUpgrade` disables future cluster upgrades and is irreversible.

## Scheduler Profile

On OpenShift 4.19, enable DRA in the cluster scheduler:

```console
$ oc patch --type merge -p '{"spec":{"profile": "HighNodeUtilization", "profileCustomizations": {"dynamicResourceAllocation": "Enabled"}}}' scheduler cluster
```

## NVIDIA GPU Operator

Install the NVIDIA GPU Operator for OpenShift. Ensure you have version 25.10.0 or later. For installation steps, see
[NVIDIA GPU Operator on Red Hat OpenShift Container Platform](https://docs.nvidia.com/datacenter/cloud-native/openshift/latest/index.html).

Ensure CDI (Container Device Interface) mode is enabled in the `ClusterPolicy`:

```yaml
cdi:
  enabled: true
```

## NVIDIA Binaries on RHCOS

NVIDIA binary locations on OpenShift nodes differ from defaults. Set the following value when installing the Helm chart:

```yaml
nvidiaDriverRoot: /run/nvidia/driver
```

Example using `--set`:

```console
$ helm install nvidia-dra-driver-gpu nvidia/nvidia-dra-driver-gpu \
    <...>
    --set nvidiaDriverRoot=/run/nvidia/driver
```
