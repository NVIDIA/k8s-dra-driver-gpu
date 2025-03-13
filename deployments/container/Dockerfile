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

ARG GOLANG_VERSION=1.23.1
# We use an ubuntu20.04 base image to allow for a more efficient multi-arch builds.
FROM --platform=${BUILDOS}/amd64 nvcr.io/nvidia/cuda:12.8.1-base-ubuntu20.04 AS build

RUN apt-get update && \
    apt-get install -y wget make git gcc-aarch64-linux-gnu gcc \
    && \
    rm -rf /var/lib/apt/lists/*

ARG GOLANG_VERSION=x.x.x
RUN wget -nv -O - https://storage.googleapis.com/golang/go${GOLANG_VERSION}.linux-amd64.tar.gz \
    | tar -C /usr/local -xz

ENV GOPATH /go
ENV PATH $GOPATH/bin:/usr/local/go/bin:$PATH

WORKDIR /build
COPY . .

RUN mkdir /artifacts
ARG VERSION="N/A"
ARG GIT_COMMIT="unknown"
ARG TARGETARCH

RUN if [ "$TARGETARCH" = "amd64" ]; then \
        cc=gcc; \
    elif [ "$TARGETARCH" = "arm64" ]; then \
        cc=aarch64-linux-gnu-gcc; \
    fi && \
    make CC=${cc} GOARCH=${TARGETARCH} PREFIX=/artifacts cmds

FROM nvcr.io/nvidia/cuda:12.8.1-base-ubi9

ENV NVIDIA_DISABLE_REQUIRE="true"
ENV NVIDIA_VISIBLE_DEVICES=all
ENV NVIDIA_DRIVER_CAPABILITIES=utility
ARG VERSION="N/A"
ARG GIT_COMMIT="unknown"

LABEL io.k8s.display-name="NVIDIA DRA Driver"
LABEL name="NVIDIA DRA Driver"
LABEL vendor="NVIDIA"
LABEL version=${VERSION}
LABEL com.nvidia.git-commit="${GIT_COMMIT}"
LABEL release="N/A"
LABEL summary="NVIDIA DRA driver for Kubernetes"
LABEL description="See summary"
LABEL org.opencontainers.image.description "NVIDIA GPU DRA driver for Kubernetes"
LABEL org.opencontainers.image.source "https://github.com/NVIDIA/k8s-dra-driver-gpu"

RUN mkdir /licenses && mv /NGC-DL-CONTAINER-LICENSE /licenses/NGC-DL-CONTAINER-LICENSE

COPY --from=build /artifacts/compute-domain-controller     /usr/bin/compute-domain-controller
COPY --from=build /artifacts/compute-domain-kubelet-plugin /usr/bin/compute-domain-kubelet-plugin
COPY --from=build /artifacts/gpu-kubelet-plugin            /usr/bin/gpu-kubelet-plugin
COPY --from=build /build/templates                         /templates
