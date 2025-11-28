# Copyright (c) 2021-2022, NVIDIA CORPORATION.  All rights reserved.
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

BUILD_MULTI_ARCH_IMAGES ?= no
DOCKER ?= docker
REGCTL ?= regctl
PUSH_ON_BUILD ?= false
ATTACH_ATTESTATIONS ?= false

##### Global variables #####
include $(CURDIR)/versions.mk

ifeq ($(IMAGE_NAME),)
IMAGE_NAME := $(REGISTRY)/$(DRIVER_NAME)
endif

# Note: this is sometimes a v-prefixed version string
# (in case of proper releases) or sometimes a commit hash
# (in case of regular CI builds) in which case VERSION
# is not v-prefixed.
IMAGE_VERSION := $(VERSION)

IMAGE_TAG ?= $(IMAGE_VERSION)
IMAGE = $(IMAGE_NAME):$(IMAGE_TAG)

##### Public rules #####
.PHONY: build

ifeq ($(BUILD_MULTI_ARCH_IMAGES),true)
BUILDX = buildx
DOCKER_BUILD_OPTIONS = --output=type=image,push=$(PUSH_ON_BUILD) --provenance=$(ATTACH_ATTESTATIONS) --sbom=$(ATTACH_ATTESTATIONS)
DOCKER_BUILD_PLATFORM_OPTIONS = --platform=linux/amd64,linux/arm64
else
# We also support building single-platform images.
# These are mostly used for local testing and as such we don't allow these to
# be pushed on build.
ifeq ($(PUSH_ON_BUILD),true)
$(error PUSH_ON_BUILD=true is not supported for single architecture builds)
endif
ARCH ?= $(shell uname -m | sed -e 's,aarch64,arm64,' -e 's,x86_64,amd64,')
DOCKER_BUILD_PLATFORM_OPTIONS = --platform=linux/$(ARCH)
endif

DOCKERFILE = $(CURDIR)/deployments/container/Dockerfile

ifeq ($(GITHUB_ACTIONS), true)
  DOCKER_BUILD_CACHE_OPTIONS = 	--cache-to type=registry,ref=ghcr.io/nvidia/k8s-dra-driver-gpu:buildcache,mode=max \
		--cache-from type=registry,ref=ghcr.io/nvidia/k8s-dra-driver-gpu:buildcache
endif

# Build the container image
build:
	DOCKER_BUILDKIT=1 \
		$(DOCKER) buildx build --pull \
		$(DOCKER_BUILD_OPTIONS) \
		$(DOCKER_BUILD_PLATFORM_OPTIONS) \
		$(DOCKER_BUILD_CACHE_OPTIONS) \
		--tag $(IMAGE) \
		--build-arg GOLANG_VERSION="$(GOLANG_VERSION)" \
		--build-arg BASH_STATIC_GIT_REF="$(BASH_STATIC_GIT_REF)" \
		--build-arg TOOLKIT_CONTAINER_IMAGE="$(TOOLKIT_CONTAINER_IMAGE)" \
		--build-arg VERSION="$(VERSION)" \
		--build-arg GIT_COMMIT="$(GIT_COMMIT)" \
		--progress=plain \
		-f $(DOCKERFILE) \
		$(CURDIR)
