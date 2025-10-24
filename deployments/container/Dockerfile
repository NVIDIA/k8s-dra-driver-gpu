# syntax=docker.io/docker/dockerfile:1.7-labs
# The above enables the COPY --exclude flag, see:
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
FROM --platform=$BUILDPLATFORM nvcr.io/nvidia/base/ubuntu:jammy-20250619 AS build

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
RUN apt-get update && \
    apt-get install -y \
        wget \
        make \
        git \
        gcc-aarch64-linux-gnu \
        gcc

# Install dependencies for `bash-static` build.
RUN apt-get install -y gpg curl autoconf file

# Build static bash binary (against musl).
WORKDIR /bashbuild
RUN git clone https://github.com/robxu9/bash-static/

# Note: cross-compilation in `build.sh` isn't robust. Fix that
# by manually setting CC, and also strip command must conditionally
# be replaced by an aarch64-specific version.
RUN ARCH="$TARGETARCH" && STRIP=strip && \
    [ "$ARCH" = "arm64" ] && ARCH="aarch64" || true && \
    [ "$ARCH" = "amd64" ] && ARCH="x86_64" || true && \
    [ "$ARCH" = "aarch64" ] && STRIP="aarch64-linux-gnu-strip" && CC="aarch64-linux-gnu-gcc" || true && \
    echo "detected arch: $ARCH" && \
    echo "cc to use: $CC" && \
    echo "strip to use: $STRIP" && \
    cd bash-static && git checkout ${BASH_STATIC_GIT_REF} && \
    sed -i 's|https://ftp\.gnu\.org/gnu|https://ftpmirror.gnu.org/|g' ./build.sh && \
    sed -i 's/-sLO/-sSfLO --retry 300 --connect-timeout 20 --retry-delay 5 --retry-all-errors /g' ./build.sh && \
    sed -i 's/strip/$STRIP/g' ./build.sh && \
    sed -i 's/make -s \&\& make -s tests/make -j4/g' ./build.sh && \
    bash version-52.sh && STRIP=$STRIP CC=$CC ./build.sh linux $ARCH

# With above's commit, this emits
# 'GNU bash, version 5.2.37(1)-release (aarch64-unknown-linux-musl)'
RUN cd /bashbuild/bash-static/releases && ./bash*-static --version
RUN mv /bashbuild/bash-static/releases/bash-*-static /bashbuild/bash
# Capture actual file architecture in the build output.
RUN file /bashbuild/bash

RUN wget -nv -O - https://storage.googleapis.com/golang/go${GOLANG_VERSION}.linux-${BUILDARCH}.tar.gz \
    | tar -C /usr/local -xz

ENV GOPATH=/go
ENV PATH=$GOPATH/bin:/usr/local/go/bin:$PATH

WORKDIR /build

# Copy everything that is needed for the go build, but do not invalidate go
# build artifact layer unnecessarily often. Note that --exclude only affects the
# cache.
COPY    --exclude=./templates/** \
        --exclude=./deployments/helm/** \
        --exclude=./hack/kubelet-plugin-prestart.sh \
        --exclude=./demo/** \
        --exclude=./Dockerfile \
        . .

RUN mkdir /artifacts

# The VERSION and GIT_COMMIT env vars are consumed by the `make` target below.
ARG VERSION="N/A"
ARG GIT_COMMIT="unknown"
RUN if [ "$TARGETARCH" = "amd64" ]; then \
        cc=gcc; \
    elif [ "$TARGETARCH" = "arm64" ]; then \
        cc=aarch64-linux-gnu-gcc; \
    fi && \
    make CC=${cc} GOARCH=${TARGETARCH} PREFIX=/artifacts cmds

# Pull the nvidia-cdi-hook binary out of the relevant toolkit container
# (arch: TARGETPLATFORM, set via --platform).
FROM ${TOOLKIT_CONTAINER_IMAGE} AS toolkit

# Construct production image (arch: TARGETPLATFORM, set via the `--platform` CLI
# arg). Note that nvcr.io/nvidia/distroless/cc is based on
# https://github.com/GoogleContainerTools/distroless; specifically on debian12.
# For consistency, the build stage above derives from Debian 12 directly. The
# `-dev` suffic is to get busybox as a shell added. For RUN directives to pick
# that up, use `SHELL ["/busybox/sh", "-c"]`.
FROM nvcr.io/nvidia/distroless/cc:v3.2.0-dev

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

COPY --from=toolkit /artifacts/rpm/usr/bin/nvidia-cdi-hook   /usr/bin/nvidia-cdi-hook
COPY --from=build   /bashbuild/bash                          /bin/bash
COPY --from=build   /artifacts/compute-domain-controller     /usr/bin/compute-domain-controller
COPY --from=build   /artifacts/compute-domain-kubelet-plugin /usr/bin/compute-domain-kubelet-plugin
COPY --from=build   /artifacts/compute-domain-daemon         /usr/bin/compute-domain-daemon
COPY --from=build   /artifacts/gpu-kubelet-plugin            /usr/bin/gpu-kubelet-plugin
COPY --from=build   /artifacts/webhook                       /usr/bin/webhook
COPY /hack/kubelet-plugin-prestart.sh /usr/bin/kubelet-plugin-prestart.sh
COPY /templates /templates

# Use root by default (for example, the init container as of now needs
# this, otherwise `ln: /driver-root: Permission denied`).
COPY --from=build /etc/passwd /etc/passwd
COPY --from=build /etc/group /etc/group
USER root:root

# Smoke-test executables (provide early build feedback).
RUN ["/usr/bin/compute-domain-kubelet-plugin", "--version"]
RUN ["/bin/bash", "--version"]
