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
BUILDX =

ifeq ($(BUILD_MULTI_ARCH_IMAGES),true)
BUILDX = buildx
endif

MKDIR    ?= mkdir

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

OUT_IMAGE_NAME ?= $(IMAGE_NAME)
OUT_IMAGE_VERSION ?= $(IMAGE_VERSION)
OUT_IMAGE_TAG = $(OUT_IMAGE_VERSION)
OUT_IMAGE = $(OUT_IMAGE_NAME):$(OUT_IMAGE_TAG)

##### Public rules #####
# Direct targets without distribution abstraction
.PHONY: image build push test push-short

ifneq ($(BUILD_MULTI_ARCH_IMAGES),true)
include $(CURDIR)/deployments/container/native-only.mk
else
include $(CURDIR)/deployments/container/multi-arch.mk
endif

# For the main push target we also push a short tag equal to the version.
# We skip this for the development release
DEVEL_RELEASE_IMAGE_VERSION ?= devel
PUSH_MULTIPLE_TAGS ?= true
ifeq ($(strip $(OUT_IMAGE_VERSION)),$(DEVEL_RELEASE_IMAGE_VERSION))
PUSH_MULTIPLE_TAGS = false
endif
ifeq ($(PUSH_MULTIPLE_TAGS),true)
push: push-short
endif

DOCKERFILE = $(CURDIR)/deployments/container/Dockerfile

# Build the container image
image:
	DOCKER_BUILDKIT=1 \
		$(DOCKER) $(BUILDX) build --pull \
		--provenance=false --sbom=false \
		$(DOCKER_BUILD_OPTIONS) \
		$(DOCKER_BUILD_PLATFORM_OPTIONS) \
		--tag $(IMAGE) \
		--build-arg GOLANG_VERSION="$(GOLANG_VERSION)" \
		--build-arg BASH_STATIC_GIT_REF="$(BASH_STATIC_GIT_REF)" \
		--build-arg TOOLKIT_CONTAINER_IMAGE="$(TOOLKIT_CONTAINER_IMAGE)" \
		--build-arg VERSION="$(VERSION)" \
		--build-arg GIT_COMMIT="$(GIT_COMMIT)" \
		--progress=plain \
		-f $(DOCKERFILE) \
		$(CURDIR)

# Push the image to the registry
push:
	$(REGCTL) \
		image copy \
		$(IMAGE) $(OUT_IMAGE)

push-short:
	$(REGCTL) \
		image copy \
		$(IMAGE) $(OUT_IMAGE_NAME):$(OUT_IMAGE_VERSION)
