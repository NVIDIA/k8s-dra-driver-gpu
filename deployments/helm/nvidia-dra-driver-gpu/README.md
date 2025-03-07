# nvidia-dra-driver-gpu

Dynamic Resource Allocation (DRA) for NVIDIA GPUs in Kubernetes

![Version: v25.3.0](https://img.shields.io/badge/Version-v25.3.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: v25.3.0](https://img.shields.io/badge/AppVersion-v25.3.0-informational?style=flat-square)

## Dynamic Resource Allocation (DRA) for NVIDIA GPUs in Kubernetes

This DRA resource driver is currently under active development and not yet designed for production use.
We may (at times) decide to push commits over main until we have something more stable. Use at your own risk.

Detailed info in [README.md](../../README.md)

## Introduction

This chart bootstraps a DRA driver deployment on a [Kubernetes](https://kubernetes.io/) cluster using the [Helm](https://helm.sh/) package manager.

## Installing the Chart

To install the chart with the release name `nvidia-dra-driver-gpu` in namespace `nvidia` from [deployments/helm/nvidia-dra-driver-gpu](./deployments/helm/nvidia-dra-driver-gpu) directory:

```console
$ helm upgrade -i --create-namespace --namespace nvidia nvidia-dra-driver-gpu .
```

> Note: The above command assumes you are in the folder where the chart is located and will install it with the default values.. The [Values](#values) section lists the parameters that can be configured during installation.

> Note: You will need to update the `values.yaml` file to set image repository and tag to the desired version from github packages.

> Note: The controller is designed to run as a single instance per cluster. Please avoid running multiple instances of the controller in the same cluster at same time.

## Building the Chart

The [package-helm-charts.sh](./hack/package-helm-charts) helper script can be used to build the helm chart

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| allowDefaultNamespace | bool | `false` |  |
| controller.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].key | string | `"node-role.kubernetes.io/control-plane"` |  |
| controller.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].operator | string | `"Exists"` |  |
| controller.containers.computeDomain.resources | object | `{}` |  |
| controller.containers.computeDomain.securityContext | object | `{}` |  |
| controller.nodeSelector | object | `{}` |  |
| controller.podAnnotations | object | `{}` |  |
| controller.podSecurityContext | object | `{}` |  |
| controller.priorityClassName | string | `"system-node-critical"` |  |
| controller.tolerations[0].effect | string | `"NoSchedule"` |  |
| controller.tolerations[0].key | string | `"node-role.kubernetes.io/control-plane"` |  |
| controller.tolerations[0].operator | string | `"Exists"` |  |
| fullnameOverride | string | `""` |  |
| gpuResourcesEnabledOverride | bool | `false` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.repository | string | `"nvcr.io/nvidia/cloud-native/k8s-dra-driver-gpu"` |  |
| image.tag | string | `""` |  |
| imagePullSecrets | list | `[]` |  |
| kubeletPlugin.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].key | string | `"feature.node.kubernetes.io/pci-10de.present"` |  |
| kubeletPlugin.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].operator | string | `"In"` |  |
| kubeletPlugin.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[0].matchExpressions[0].values[0] | string | `"true"` |  |
| kubeletPlugin.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[1].matchExpressions[0].key | string | `"feature.node.kubernetes.io/cpu-model.vendor_id"` |  |
| kubeletPlugin.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[1].matchExpressions[0].operator | string | `"In"` |  |
| kubeletPlugin.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[1].matchExpressions[0].values[0] | string | `"NVIDIA"` |  |
| kubeletPlugin.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[2].matchExpressions[0].key | string | `"nvidia.com/gpu.present"` |  |
| kubeletPlugin.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[2].matchExpressions[0].operator | string | `"In"` |  |
| kubeletPlugin.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution.nodeSelectorTerms[2].matchExpressions[0].values[0] | string | `"true"` |  |
| kubeletPlugin.containers.computeDomains.resources | object | `{}` |  |
| kubeletPlugin.containers.computeDomains.securityContext.privileged | bool | `true` |  |
| kubeletPlugin.containers.gpus.resources | object | `{}` |  |
| kubeletPlugin.containers.gpus.securityContext.privileged | bool | `true` |  |
| kubeletPlugin.containers.init.resources | object | `{}` |  |
| kubeletPlugin.containers.init.securityContext | object | `{}` |  |
| kubeletPlugin.nodeSelector | object | `{}` |  |
| kubeletPlugin.podAnnotations | object | `{}` |  |
| kubeletPlugin.podSecurityContext | object | `{}` |  |
| kubeletPlugin.priorityClassName | string | `"system-node-critical"` |  |
| kubeletPlugin.tolerations | list | `[]` |  |
| kubeletPlugin.updateStrategy.type | string | `"RollingUpdate"` |  |
| nameOverride | string | `""` |  |
| namespaceOverride | string | `""` |  |
| nvidiaCtkPath | string | `"/usr/bin/nvidia-ctk"` |  |
| nvidiaDriverRoot | string | `"/"` |  |
| resources.computeDomains.enabled | bool | `true` |  |
| resources.gpus.enabled | bool | `true` |  |
| selectorLabelsOverride | object | `{}` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
