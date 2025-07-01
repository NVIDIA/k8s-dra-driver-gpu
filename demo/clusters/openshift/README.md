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
