# syntax=docker.io/docker/dockerfile:1.7-labs
# The above enables the COPY --exclude flag, and the --parent flag see:
# https://github.com/moby/buildkit/pull/4561

# Copyright (c) 2019-2025, NVIDIA CORPORATION.  All rights reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Declared here for use in FROM directives below
ARG TOOLKIT_CONTAINER_IMAGE=unknown

# Run build with binaries native to the current build platform.
FROM --platform=$BUILDPLATFORM nvcr.io/nvidia/base/ubuntu:jammy-20251013 AS build

# Require arg to be provided (set invalid default value).
ARG GOLANG_VERSION=x.x.x
ARG BASH_STATIC_GIT_REF=unknown

# BUILDARCH, TARGETARCH (and others) are defined in the global scope by
# BuiltKit. BUILDARCH is the architecture of the build platform. TARGETARCH is
# set via the --platform arg provided to the `docker buildx build ...` command.
# Redefining those variables here without new values makes the outer-context
# values available to in-stage RUN commands. Arch values are of the form
# amd64/arm64.
ARG BUILDARCH
ARG TARGETARCH

# Install dependencies for Go build. Do not clear apt cache (does not
# leak into prod stage).
RUN apt-get update
RUN apt-get install -y \
        wget time make \
        gcc \
        gcc-aarch64-linux-gnu

RUN wget -nv -O - https://storage.googleapis.com/golang/go${GOLANG_VERSION}.linux-${BUILDARCH}.tar.gz \
    | tar -C /usr/local -xz

ENV GOPATH=/go
ENV GOCACHE=/go/build-cache
ENV PATH=$GOPATH/bin:/usr/local/go/bin:$PATH

RUN mkdir deployments && mkdir hack
RUN mkdir /artifacts

# Copy precisely only what is needed for the go build: only invalidate go
# build artifact layer when required.
WORKDIR /build
COPY --parents cmd api pkg vendor internal ./
COPY common.mk versions.mk Makefile go.mod go.sum ./
COPY deployments/devel deployments/devel
COPY hack/golang-version.sh hack/golang-version.sh
COPY hack/toolkit-container-image.sh hack/toolkit-container-image.sh

# The VERSION and GIT_COMMIT env vars are consumed by the `make` target below.
# Changing input invalidates the cached image layer.
ARG VERSION="N/A"
ARG GIT_COMMIT="unknown"

# Use Go build cache across container image builds. Maintained by docker/buildx
# by using a persistent volume dedicated for this cache (automatically mounted
# into the container at the specified target path whenever that RUN instruction
# is executed). Note that `--mount=type=cache` directories are also included
# when exporting/importing the layer cache cache using `--cache-to/from`.
# Without this cache, on my machine, the builds takes 1-2 minutes for trivial
# code changes. With the cache, the build takes less than 10 seconds.
RUN --mount=type=cache,target=${GOCACHE} \
    if [ "$TARGETARCH" = "amd64" ]; then \
        cc=gcc; \
    elif [ "$TARGETARCH" = "arm64" ]; then \
        cc=aarch64-linux-gnu-gcc; \
    fi && \
    time make CC=${cc} GOARCH=${TARGETARCH} PREFIX=/artifacts cmds

# Build static bash binary (against musl). Notes: cross-compilation isn't
# robust (see non-deterministic segfaults tracked in issues/502; source code
# download instabilities tracked in issues/743) and generally takes signifiant
# time even on happy path (dominiates CI duration as of the time of writing). Do
# not build this every time. Instead, consume it from a previous image on GHCR
# -- also, leave machinery (out-commented) in place.
#
# Choose: 1) fresh bash build or 2) use binary from previous image
#
# option 1:
# WORKDIR /bashbuild
# COPY hack/build-bash-static.sh .
# RUN export BASH_STATIC_GIT_REF && bash /bashbuild/build-bash-static.sh
# RUN cd /bashbuild/bash-static/releases && ./bash*-static --version && mv bash-*-static /bashbuild/bash && file /bashbuild/bash

# option 2:
FROM ghcr.io/nvidia/k8s-dra-driver-gpu:v25.12.0-dev-839e966a AS bash

# Pull the nvidia-cdi-hook binary out of the relevant toolkit container
# (arch: TARGETPLATFORM, set via --platform).
FROM ${TOOLKIT_CONTAINER_IMAGE} AS toolkit

# Construct production image (arch: TARGETPLATFORM, set via the `--platform` CLI
# arg). Note that nvcr.io/nvidia/distroless/cc is based on
# https://github.com/GoogleContainerTools/distroless; specifically on debian12.
# For consistency, the build stage above derives from Debian 12 directly. The
# `-dev` suffic is to get busybox as a shell added. For RUN directives to pick
# that up, use `SHELL ["/busybox/sh", "-c"]`.
FROM nvcr.io/nvidia/distroless/cc:v3.2.1-dev

ENV NVIDIA_DISABLE_REQUIRE="true"
ENV NVIDIA_VISIBLE_DEVICES=all
ENV NVIDIA_DRIVER_CAPABILITIES=utility
ARG VERSION="N/A"
ARG GIT_COMMIT="unknown"

LABEL io.k8s.display-name="NVIDIA DRA Driver for GPUs"
LABEL name="NVIDIA DRA Driver for GPUs"
LABEL vendor="NVIDIA"
LABEL version=${VERSION}
LABEL com.nvidia.git-commit="${GIT_COMMIT}"
LABEL release="N/A"
LABEL summary="NVIDIA DRA Driver for GPUs"
LABEL description="NVIDIA DRA Driver for GPUs"
LABEL org.opencontainers.image.description="NVIDIA DRA Driver for GPUs"
LABEL org.opencontainers.image.source="https://github.com/NVIDIA/k8s-dra-driver-gpu"

# Add top-level license (AL2) file into the container image
COPY LICENSE /

# option 1: fresh bash build
#COPY --from=build   /bashbuild/bash                           /bin/bash

# option 2: bash from previous image
COPY --from=bash    /bin/bash                                 /bin/bash

COPY --from=toolkit /artifacts/rpm/usr/bin/nvidia-cdi-hook    /usr/bin/nvidia-cdi-hook
COPY --from=build   /artifacts/compute-domain-controller      /usr/bin/compute-domain-controller
COPY --from=build   /artifacts/compute-domain-kubelet-plugin  /usr/bin/compute-domain-kubelet-plugin
COPY --from=build   /artifacts/compute-domain-daemon          /usr/bin/compute-domain-daemon
COPY --from=build   /artifacts/gpu-kubelet-plugin             /usr/bin/gpu-kubelet-plugin
COPY --from=build   /artifacts/webhook                        /usr/bin/webhook

COPY /scripts/bind_to_driver.sh                               /usr/bin/bind_to_driver.sh
COPY /scripts/unbind_from_driver.sh                           /usr/bin/unbind_from_driver.sh
COPY /hack/kubelet-plugin-prestart.sh                         /usr/bin/kubelet-plugin-prestart.sh
COPY /templates /templates

# Use root by default (for example, the init container as of now needs
# this, otherwise `ln: /driver-root: Permission denied`).
COPY --from=build /etc/passwd /etc/passwd
COPY --from=build /etc/group /etc/group
USER root:root

# Smoke-test executables (provide early build feedback).
RUN ["/usr/bin/compute-domain-kubelet-plugin", "--version"]
RUN ["/bin/bash", "--version"]
