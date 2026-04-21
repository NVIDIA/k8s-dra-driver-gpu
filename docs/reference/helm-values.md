# Helm chart values

All configurable values for the `nvidia-dra-driver-gpu` Helm chart. To see defaults inline:

```bash
helm show values nvidia/nvidia-dra-driver-gpu
```

---

## Core

| Parameter | Default | Description |
|---|---|---|
| `nvidiaDriverRoot` | `/` | Path to the GPU driver root on the host. Set to `/run/nvidia/driver` when using the NVIDIA GPU Driver Container (operator-managed driver). |
| `nvidiaCDIHookPath` | `""` | Path to the `nvidia-cdi-hook` executable. If empty, the path is inferred from the installed nvidia-container-toolkit version. |
| `resourceApiVersion` | `""` | Override the `resource.k8s.io` API version used for DRA resources. Leave empty to auto-detect from the cluster (`v1` > `v1beta2` > `v1beta1`). |
| `logVerbosity` | `"4"` | Log verbosity for all components. `0` = errors/warnings/info only; `6` = GRPC and API request detail; `7` = health check output. |
| `allowDefaultNamespace` | `false` | Allow the chart to install into the `default` namespace. |

---

## Image

| Parameter | Default | Description |
|---|---|---|
| `image.repository` | `registry.k8s.io/dra-driver-nvidia/dra-driver-nvidia-gpu` | Container image repository. |
| `image.tag` | `""` | Image tag. Empty resolves to the chart `appVersion` with a `v` prefix. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | `[]` | List of image pull secret names. |

---

## Resources

| Parameter | Default | Description |
|---|---|---|
| `resources.gpus.enabled` | `true` | Enable the GPU kubelet plugin and associated `DeviceClass` objects (`gpu.nvidia.com`, `mig.nvidia.com`, `vfio.gpu.nvidia.com`). |
| `resources.computeDomains.enabled` | `true` | Enable the ComputeDomain controller, kubelet plugin, and associated `DeviceClass` objects. |

---

## Feature gates

Set feature gates as a map of gate name to boolean:

```yaml
featureGates:
  TimeSlicingSettings: true
  MPSSupport: true
```

| Parameter | Default | Description |
|---|---|---|
| `featureGates` | `{}` | Map of feature gate name to boolean. See [Feature gates](feature-gates.md) for available gates and their stages. |

---

## Webhook

The admission webhook validates opaque configuration in `ResourceClaim` and `ResourceClaimTemplate` specs. Disabled by default; requires [cert-manager](https://cert-manager.io/) unless using `tls.mode: secret`.

| Parameter | Default | Description |
|---|---|---|
| `webhook.enabled` | `false` | Enable the admission webhook. |
| `webhook.replicas` | `1` | Number of webhook pod replicas. |
| `webhook.servicePort` | `443` | Service port for the webhook. |
| `webhook.containerPort` | `443` | Container port for the webhook. |
| `webhook.priorityClassName` | `system-cluster-critical` | Pod priority class. |
| `webhook.failurePolicy` | `Fail` | How the API server handles webhook call failures: `Fail` rejects the request, `Ignore` allows it. |
| `webhook.tls.mode` | `cert-manager` | TLS certificate source: `cert-manager` or `secret`. |
| `webhook.tls.certManager.issuerType` | `selfsigned` | cert-manager issuer type: `selfsigned`, `clusterissuer`, or `issuer`. |
| `webhook.tls.certManager.issuerName` | `""` | Issuer name. Required when `issuerType` is `clusterissuer` or `issuer`. |
| `webhook.tls.secret.name` | `""` | Name of an existing secret containing `tls.crt` and `tls.key`. Used when `tls.mode: secret`. |
| `webhook.tls.secret.caBundle` | `""` | Base64-encoded CA bundle for validating the webhook TLS certificate. Required when `tls.mode: secret`. |
| `webhook.nodeSelector` | `{}` | Node selector for webhook pods. |
| `webhook.tolerations` | `[]` | Tolerations for webhook pods. |
| `webhook.affinity` | `{}` | Affinity rules for webhook pods. |
| `webhook.podAnnotations` | `{}` | Annotations added to webhook pods. |

---

## Controller

The `compute-domain-controller` runs as a cluster-scoped Deployment. It watches `ComputeDomain` CRs and spawns per-domain DaemonSets.

| Parameter | Default | Description |
|---|---|---|
| `controller.replicas` | `1` | Number of controller pod replicas. |
| `controller.priorityClassName` | `system-node-critical` | Pod priority class. |
| `controller.leaderElection.enabled` | `false` | Enable leader election (required for `replicas > 1`). |
| `controller.leaderElection.leaseDuration` | `15s` | Leader election lease duration. |
| `controller.leaderElection.renewDeadline` | `10s` | Leader election renew deadline. |
| `controller.leaderElection.retryPeriod` | `2s` | Leader election retry period. |
| `controller.metrics.enabled` | `true` | Expose Prometheus metrics. |
| `controller.metrics.httpEndpoint` | `:8080` | Address and port for the metrics endpoint. |
| `controller.metrics.metricsPath` | `/metrics` | HTTP path for metrics. |
| `controller.nodeSelector` | `{}` | Node selector for controller pods. |
| `controller.tolerations` | control-plane/master taint tolerations | Tolerations for controller pods. |
| `controller.affinity` | control-plane node affinity | Affinity rules for controller pods. |
| `controller.podAnnotations` | `{}` | Annotations added to controller pods. |
| `controller.networkPolicy.enabled` | `false` | Create a `NetworkPolicy` for the controller. |

---

## Kubelet plugin

The kubelet plugin DaemonSet runs on every GPU node. It contains two containers: one for GPU devices (`gpus`) and one for ComputeDomain devices (`computeDomains`).

| Parameter | Default | Description |
|---|---|---|
| `kubeletPlugin.priorityClassName` | `system-node-critical` | Pod priority class. |
| `kubeletPlugin.updateStrategy.type` | `RollingUpdate` | DaemonSet update strategy. |
| `kubeletPlugin.kubeletRegistrarDirectoryPath` | `/var/lib/kubelet/plugins_registry` | Path to the kubelet plugin registrar directory. Change only if your kubelet uses a non-standard path. |
| `kubeletPlugin.kubeletPluginsDirectoryPath` | `/var/lib/kubelet/plugins` | Path to the kubelet plugins directory. |
| `kubeletPlugin.metrics.enabled` | `true` | Expose Prometheus metrics. |
| `kubeletPlugin.metrics.gpuHttpEndpoint` | `:8080` | Metrics address for the GPU plugin container. |
| `kubeletPlugin.metrics.computeDomainHttpEndpoint` | `:8081` | Metrics address for the ComputeDomain plugin container. |
| `kubeletPlugin.metrics.metricsPath` | `/metrics` | HTTP path for metrics. |
| `kubeletPlugin.containers.gpus.healthcheckPort` | `51516` | gRPC health service port for the GPU plugin container. Set to a negative value to disable. |
| `kubeletPlugin.containers.computeDomains.healthcheckPort` | `51515` | gRPC health service port for the ComputeDomain plugin container. Set to a negative value to disable. |
| `kubeletPlugin.nodeSelector` | `{}` | Additional node selector for the DaemonSet (extends the built-in GPU node affinity). |
| `kubeletPlugin.tolerations` | `[]` | Tolerations for kubelet plugin pods. |
| `kubeletPlugin.affinity` | NVIDIA GPU node affinity | Affinity rules. By default targets nodes with `feature.node.kubernetes.io/pci-10de.present=true`, `cpu-model.vendor_id=NVIDIA`, or `nvidia.com/gpu.present=true`. |
| `kubeletPlugin.podAnnotations` | `{}` | Annotations added to kubelet plugin pods. |
| `kubeletPlugin.networkPolicy.enabled` | `false` | Create a `NetworkPolicy` for the kubelet plugin DaemonSet. |

---

## Service account

| Parameter | Default | Description |
|---|---|---|
| `serviceAccount.create` | `true` | Create a service account for the driver. |
| `serviceAccount.name` | `""` | Name of the service account. Auto-generated if empty. |
| `serviceAccount.annotations` | `{}` | Annotations added to the service account. |
