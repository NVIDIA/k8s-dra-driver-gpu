# k8s-dra-driver

Dynamic Resource Allocation (DRA) for NVIDIA GPUs in Kubernetes

![Version: 0.1.0](https://img.shields.io/badge/Version-0.1.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

## Dynamic Resource Allocation (DRA) for NVIDIA GPUs in Kubernetes

This DRA resource driver is currently under active development and not yet designed for production use.
We may (at times) decide to push commits over main until we have something more stable. Use at your own risk.

Detailed info in [README.md](../../README.md)

## Introduction

This chart bootstraps a DRA driver deployment on a [Kubernetes](https://kubernetes.io/) cluster using the [Helm](https://helm.sh/) package manager.

## Installing the Chart

To install the chart with the release name `nvidia-dra-driver` in namespace `nvidia` from [deployments/helm/k8s-dra-driver](./deployments/helm/k8s-dra-driver) directory:

```console
$ helm upgrade -i --create-namespace --namespace nvidia nvidia-dra-driver .
```

> Note: The above command assumes you are in the folder where the chart is located and will install it with the default values.. The [Values](#values) section lists the parameters that can be configured during installation.

> Note: You will need to update the `values.yaml` file to set image repository and tag to the desired version from github packages.

> Note: The controller is designed to run as a single instance per cluster. Please avoid running multiple instances of the controller in the same cluster at same time.

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| allowDefaultNamespace | bool | `false` |  |
| controller.affinity | object | `{}` |  |
| controller.containers.controller.resources | object | `{}` |  |
| controller.containers.controller.securityContext | object | `{}` |  |
| controller.nodeSelector | object | `{}` |  |
| controller.podAnnotations | object | `{}` |  |
| controller.podSecurityContext | object | `{}` |  |
| controller.priorityClassName | string | `"system-node-critical"` |  |
| controller.tolerations[0].effect | string | `"NoSchedule"` |  |
| controller.tolerations[0].key | string | `"node-role.kubernetes.io/master"` |  |
| controller.tolerations[0].operator | string | `"Exists"` |  |
| controller.tolerations[1].effect | string | `"NoSchedule"` |  |
| controller.tolerations[1].key | string | `"node-role.kubernetes.io/control-plane"` |  |
| controller.tolerations[1].operator | string | `"Exists"` |  |
| deviceClasses[0] | string | `"gpu"` |  |
| deviceClasses[1] | string | `"mig"` |  |
| deviceClasses[2] | string | `"imex"` |  |
| fullnameOverride | string | `""` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.repository | string | `"nvcr.io/nvidia/cloud-native/k8s-dra-driver"` |  |
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
| kubeletPlugin.containers.init.resources | object | `{}` |  |
| kubeletPlugin.containers.init.securityContext | object | `{}` |  |
| kubeletPlugin.containers.plugin.resources | object | `{}` |  |
| kubeletPlugin.containers.plugin.securityContext.privileged | bool | `true` |  |
| kubeletPlugin.nodeSelector | object | `{}` |  |
| kubeletPlugin.podAnnotations | object | `{}` |  |
| kubeletPlugin.podSecurityContext | object | `{}` |  |
| kubeletPlugin.priorityClassName | string | `"system-node-critical"` |  |
| kubeletPlugin.tolerations | list | `[]` |  |
| kubeletPlugin.updateStrategy.type | string | `"RollingUpdate"` |  |
| maskNvidiaDriverParams | bool | `false` |  |
| nameOverride | string | `""` |  |
| namespaceOverride | string | `""` |  |
| nvidiaCtkPath | string | `"/usr/bin/nvidia-ctk"` |  |
| nvidiaDriverRoot | string | `"/"` |  |
| selectorLabelsOverride | object | `{}` |  |
| serviceAccount.annotations | object | `{}` |  |
| serviceAccount.create | bool | `true` |  |
| serviceAccount.name | string | `""` |  |
