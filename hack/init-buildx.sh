#!/usr/bin/env bash

# Copyright The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Bootstrap docker buildx for multi-arch image builds

set -o errexit
set -o nounset
set -o pipefail

BUILDER_NAME="${BUILDER_NAME:-nvidia-dra-driver-gpu-builder}"

# We can skip setup if the current builder already has multi-arch
# AND if it isn't the docker driver, which doesn't work
current_builder="$(docker buildx inspect 2>/dev/null || true)"
if ! grep -Eq "^Driver:\s*docker$"  <<<"${current_builder}" && \
     grep -q "linux/amd64" <<<"${current_builder}" && \
     grep -q "linux/arm64" <<<"${current_builder}"; then
  exit 0
fi

# Ensure qemu is in binfmt_misc
# Adapted from https://github.com/kubernetes-sigs/kind/blob/main/hack/build/init-buildx.sh
BINFMT_IMAGE="${BINFMT_IMAGE:-tonistiigi/binfmt:qemu-v7.0.0@sha256:66e11bea77a5ea9d6f0fe79b57cd2b189b5d15b93a2bdb925be22949232e4e55}"
if [ "$(uname)" == 'Linux' ]; then
	docker run --rm --privileged "${BINFMT_IMAGE}" --install all
fi

docker buildx rm "${BUILDER_NAME}" || true
docker buildx create --use --name="${BUILDER_NAME}"
docker buildx inspect --bootstrap
