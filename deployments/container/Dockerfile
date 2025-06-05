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
FROM --platform=$BUILDPLATFORM nvcr.io/nvidia/cuda:12.9.0-base-ubuntu20.04 AS build

# Require arg to be provided (set invalid default value).
ARG GOLANG_VERSION=x.x.x

# BUILDARCH, TARGETARCH (and others) are defined in the global scope by
# BuiltKit. BUILDARCH is the architecture of the build platform. TARGETARCH is
# set via the --platform arg provided to the `docker buildx build ...` command.
# Redefining those variables here without new values makes the outer-context
# values available to in-stage RUN commands. Arch values are of the form
# amd64/arm64.
ARG BUILDARCH
ARG TARGETARCH

RUN apt-get update && \
    apt-get install -y \
        wget \
        make \
        git \
        gcc-aarch64-linux-gnu \
        gcc && \
    rm -rf /var/lib/apt/lists/*

RUN wget -nv -O - https://storage.googleapis.com/golang/go${GOLANG_VERSION}.linux-${BUILDARCH}.tar.gz \
    | tar -C /usr/local -xz

ENV GOPATH=/go
ENV PATH=$GOPATH/bin:/usr/local/go/bin:$PATH

WORKDIR /build
COPY . .

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

# Construct production image (arch: TARGETPLATFORM, set via --platform).
FROM nvcr.io/nvidia/cuda:12.9.0-base-ubi9

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

# When doing a cross-platform build (e.g., amd64 -> arm64) then mkdir/mv below
# require virtualization. To support that you might have to install qemu:
# https://docs.docker.com/build/building/multi-platform/#install-qemu-manually
RUN mkdir /licenses && mv /NGC-DL-CONTAINER-LICENSE /licenses/NGC-DL-CONTAINER-LICENSE

COPY --from=toolkit /usr/bin/nvidia-cdi-hook                 /usr/bin/nvidia-cdi-hook
COPY --from=build   /artifacts/compute-domain-controller     /usr/bin/compute-domain-controller
COPY --from=build   /artifacts/compute-domain-kubelet-plugin /usr/bin/compute-domain-kubelet-plugin
COPY --from=build   /artifacts/compute-domain-daemon         /usr/bin/compute-domain-daemon
COPY --from=build   /artifacts/gpu-kubelet-plugin            /usr/bin/gpu-kubelet-plugin
COPY --from=build   /build/hack/kubelet-plugin-prestart.sh   /usr/bin/kubelet-plugin-prestart.sh
COPY --from=build   /build/templates                         /templates
