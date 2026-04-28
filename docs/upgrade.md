# Upgrade

!!! warning
    Skipping the CRD update step can break running workloads. Follow the steps below in order.

When upgrading between stable releases, the driver guarantees that:

- Existing workloads keep running during the upgrade.
- Existing workloads can be deleted after the upgrade.

## Before you start

- Identify your target release version from the [release tags](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/tags).
- Have `kubectl` and `helm` configured for your cluster.

## Steps

### 1. Update Custom Resource Definitions (CRDs)

Apply both CRD files for the target release. Applying both is always safe — `kubectl apply` is idempotent.

Replace `<version>` with the target release tag (for example, `v25.12.0`):

```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/dra-driver-nvidia-gpu/refs/tags/<version>/deployments/helm/dra-driver-nvidia-gpu/crds/resource.nvidia.com_computedomains.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-sigs/dra-driver-nvidia-gpu/refs/tags/<version>/deployments/helm/dra-driver-nvidia-gpu/crds/resource.nvidia.com_computedomaincliques.yaml
```

### 2. Upgrade the Helm chart

```bash
helm upgrade --install nvidia-dra-driver-gpu nvidia/nvidia-dra-driver-gpu \
    --version="<version>" \
    --namespace nvidia-dra-driver-gpu
```

!!! warning
    Use `helm upgrade --install`, not `helm uninstall` followed by `helm install`. Uninstalling removes service accounts from under running Kubernetes objects and disrupts active workloads.

## Downgrading

Downgrading is not officially supported. CRD schema changes between releases cannot be reliably reversed with `kubectl apply`, and rolling back the Helm chart without also reverting the CRDs may leave the cluster in an inconsistent state.

If you need to downgrade, refer to the release notes for the target version for any version-specific guidance, and test the procedure in a non-production cluster first.
