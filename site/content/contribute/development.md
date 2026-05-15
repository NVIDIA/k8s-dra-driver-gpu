---
title: Development
linkTitle: Development
weight: 10
description: Build, test, and the local-cluster iteration loop.
---

**Tooling:** Go (version per [`go.mod`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/main/go.mod)), Docker + Buildx, Helm v3, kind, golangci-lint. Codegen tools are installed by `make -C deployments/devel install-tools`.

## Make targets

```
make build         # go build ./...
make cmds          # build the five binaries in cmd/
make test          # race-enabled unit tests
make generate      # CRDs, deepcopy, clientset, listers, informers
make check         # golangci-lint + check-generate  (the CI gate)
make helm-lint     # lint the Helm chart
make bats          # BATS integration tests  (needs a live cluster)
```

Prefix any target with `docker-` to run it in the devel container (requires `BUILD_DEVEL_IMAGE=yes`).

## Local cluster

```sh
export KIND_CLUSTER_NAME=kind-dra-1
./demo/clusters/kind/build-dra-driver-gpu.sh     # build & load driver image
./demo/clusters/kind/create-cluster.sh           # create the cluster
./demo/clusters/kind/install-dra-driver-gpu.sh   # helm install
```

Iterate: edit → rerun the build script → rerun the install script.

GPU-aware variant: [`demo/clusters/nvkind/`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/tree/main/demo/clusters/nvkind). GKE recipes: [`demo/clusters/gke/`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/tree/main/demo/clusters/gke).

## Tests

- **Unit**: `make test`.
- **BATS**: [`tests/bats/`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/tree/main/tests/bats) — invasive, runs against a real cluster. Read [`tests/bats/README.md`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/main/tests/bats/README.md) first.

## CI

GitHub Actions in [`.github/workflows/`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/tree/main/.github/workflows) run lint, codegen check, unit tests, build, chart lint, and CodeQL. End-to-end GPU tests run on Prow / Lambda Cloud — see [`hack/ci/lambda/e2e-test.sh`](https://github.com/kubernetes-sigs/dra-driver-nvidia-gpu/blob/main/hack/ci/lambda/e2e-test.sh).