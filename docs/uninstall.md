# Uninstall

## Uninstall the driver

```bash
helm uninstall nvidia-dra-driver-gpu --namespace nvidia-dra-driver-gpu
```

This removes the driver deployment, service accounts, RBAC, and associated resources created by the Helm chart.

> **Note**: Custom Resource Definitions (CRDs) and any existing `ComputeDomain` objects are not removed by `helm uninstall`. Refer to [Remove CRDs](#remove-crds) below if you want to clean those up as well.

## Remove CRDs

> **Warning**: Deleting CRDs removes all `ComputeDomain` and `ComputeDomainClique` objects in the cluster. This is irreversible.

```bash
kubectl delete crd computedomains.resource.nvidia.com
kubectl delete crd computedomaincliques.resource.nvidia.com
```

<!-- TODO: Verify CRD names match exactly what is installed by the chart. Confirm helm uninstall does not remove CRDs automatically via any chart hooks. -->
