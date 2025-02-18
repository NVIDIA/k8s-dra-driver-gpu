# Dynamic Resource Allocation (DRA) for NVIDIA GPUs in Kubernetes

This DRA resource driver is currently under active development and not yet
designed for production use.
We may (at times) decide to push commits over `main` until we have something more stable.
Use at your own risk.

A document and demo of the DRA support for GPUs provided by this repo can be found below:
|                                                                                                                          Document                                                                                                                          |                                                                                                                                                                   Demo                                                                                                                                                                   |
|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------:|:----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------:|
| [<img width="300" alt="Dynamic Resource Allocation (DRA) for GPUs in Kubernetes" src="https://drive.google.com/uc?export=download&id=12EwdvHHI92FucRO2tuIqLR33OC8MwCQK">](https://docs.google.com/document/d/1BNWqgx_SmZDi-va_V31v3DnuVwYnF2EmN7D-O_fB6Oo) | [<img width="300" alt="Demo of Dynamic Resource Allocation (DRA) for GPUs in Kubernetes" src="https://drive.google.com/uc?export=download&id=1UzB-EBEVwUTRF7R0YXbGe9hvTjuKaBlm">](https://drive.google.com/file/d/1iLg2FEAEilb1dcI27TnB19VYtbcvgKhS/view?usp=sharing "Demo of Dynamic Resource Allocation (DRA) for GPUs in Kubernetes") |

## Demo

This section describes using `kind` to demo the functionality of the NVIDIA GPU DRA Driver.

First since we'll launch kind with GPU support, ensure that the following prerequisites are met:
1. `kind` is installed. See the official documentation [here](https://kind.sigs.k8s.io/docs/user/quick-start/#installation).
1. Ensure that the NVIDIA Container Toolkit is installed on your system. This
   can be done by following the instructions
   [here](https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/install-guide.html).
1. Configure the NVIDIA Container Runtime as the **default** Docker runtime:
   ```console
   sudo nvidia-ctk runtime configure --runtime=docker --set-as-default
   ```
1. Restart Docker to apply the changes:
   ```console
   sudo systemctl restart docker
   ```
1. Set the `accept-nvidia-visible-devices-as-volume-mounts` option to `true` in
   the `/etc/nvidia-container-runtime/config.toml` file to configure the NVIDIA
   Container Runtime to use volume mounts to select devices to inject into a
   container.
   ``` console
   sudo nvidia-ctk config --in-place --set accept-nvidia-visible-devices-as-volume-mounts=true
   ```

1. Show the current set of GPUs on the machine:
   ```console
   nvidia-smi -L
   ```

We start by first cloning this repository and `cd`ing into it.
All of the scripts and example Pod specs used in this demo are in the `demo`
subdirectory, so take a moment to browse through the various files and see
what's available:

```console
git clone https://github.com/NVIDIA/k8s-dra-driver.git
```
```console
cd k8s-dra-driver
```

### Setting up the infrastructure

Here's a demo showing how to install and configure DRA, and run a pod in a `kind` cluster on a Linux workstation. 

<p align="center">
<img width="800" src="./demo/specs/quickstart/basic-demo.svg">
</p>

Below are the detailed, step-by-step instructions.

First, create a `kind` cluster to run the demo:
```bash
./demo/clusters/kind/create-cluster.sh
```

From here we will build the image for the example resource driver:
```console
./demo/clusters/kind/build-dra-driver.sh
```

This also makes the built images available to the `kind` cluster.

We now install the NVIDIA GPU DRA driver:
```console
./demo/clusters/kind/install-dra-driver.sh
```

This should show two pods running in the `nvidia` namespace:
```console
kubectl get pods -n nvidia
```
```
NAME                                                          READY   STATUS    RESTARTS   AGE
nvidia-dra-driver-k8s-dra-driver-controller-844fcb94b-ktbkc   1/1     Running   0          69s
nvidia-dra-driver-k8s-dra-driver-kubelet-plugin-5vfp9         1/1     Running   0          69s
```

> Note: The controller is designed to run as a single instance per cluster. Please avoid running multiple instances of the controller in the same cluster at same time.

### Run the examples by following the steps in the demo script
Finally, you can run the various examples contained in the `demo/specs/quickstart` folder.
With the most recent updates for Kubernetes v1.31, only the first 3 examples in
this folder are currently functional.

You can run them as follows:
```console
kubectl apply --filename=demo/specs/quickstart/gpu-test{1,2,3}.yaml
```

Get the pods' statuses. Depending on which GPUs are available, running the first three examples will produce output similar to the following...

**Note:** there is a [known issue with kind](https://kind.sigs.k8s.io/docs/user/known-issues/#pod-errors-due-to-too-many-open-files). You may see an error while trying to tail the log of a running pod in the kind cluster: `failed to create fsnotify watcher: too many open files.` The issue may be resolved by increasing the value for `fs.inotify.max_user_watches`.
```console
kubectl get pod -A -l app=pod
```
```
NAMESPACE           NAME                                       READY   STATUS    RESTARTS   AGE
gpu-test1           pod1                                       1/1     Running   0          34s
gpu-test1           pod2                                       1/1     Running   0          34s
gpu-test2           pod                                        2/2     Running   0          34s
gpu-test3           pod1                                       1/1     Running   0          34s
gpu-test3           pod2                                       1/1     Running   0          34s
```
```console
kubectl logs -n gpu-test1 -l app=pod
```
```
GPU 0: A100-SXM4-40GB (UUID: GPU-662077db-fa3f-0d8f-9502-21ab0ef058a2)
GPU 0: A100-SXM4-40GB (UUID: GPU-4cf8db2d-06c0-7d70-1a51-e59b25b2c16c)
```
```console
kubectl logs -n gpu-test2 pod --all-containers
```
```
GPU 0: A100-SXM4-40GB (UUID: GPU-79a2ba02-a537-ccbf-2965-8e9d90c0bd54)
GPU 0: A100-SXM4-40GB (UUID: GPU-79a2ba02-a537-ccbf-2965-8e9d90c0bd54)
```

```console
kubectl logs -n gpu-test3 -l app=pod
```
```
GPU 0: A100-SXM4-40GB (UUID: GPU-4404041a-04cf-1ccf-9e70-f139a9b1e23c)
GPU 0: A100-SXM4-40GB (UUID: GPU-4404041a-04cf-1ccf-9e70-f139a9b1e23c)
```

### Cleaning up the environment

Remove the cluster created in the preceding steps:
```console
./demo/clusters/kind/delete-cluster.sh
```

<!--
TODO: This README should be extended with additional content including:

## Information for "real" deployment including prerequesites

This may include the following content from the original scripts:
```
set -e

export VERSION=v0.1.0

REGISTRY=nvcr.io/nvidia/cloud-native
IMAGE=k8s-dra-driver
PLATFORM=ubi8

sudo true
make -f deployments/container/Makefile build-${PLATFORM}
docker tag ${REGISTRY}/${IMAGE}:${VERSION}-${PLATFORM} ${REGISTRY}/${IMAGE}:${VERSION}
docker save ${REGISTRY}/${IMAGE}:${VERSION} > image.tgz
sudo ctr -n k8s.io image import image.tgz
```

## Information on advanced usage such as MIG.

This includes setting configuring MIG on the host using mig-parted. Some of the demo scripts included
in ./demo/ require this.

```
cat <<EOF | sudo -E nvidia-mig-parted apply -f -
version: v1
mig-configs:
half-half:
   - devices: [0,1,2,3]
      mig-enabled: false
   - devices: [4,5,6,7]
      mig-enabled: true
      mig-devices: {}
EOF
```
-->
