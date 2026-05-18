# Copyright (c) 2024, NVIDIA CORPORATION.  All rights reserved.
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

DRIVER_NAME := k8s-dra-driver-gpu
HELM_DRIVER_NAME := nvidia-dra-driver-gpu
MODULE := github.com/NVIDIA/$(DRIVER_NAME)

REGISTRY ?= nvcr.io/nvidia

VERSION  ?= v25.3.2

# vVERSION represents the version with a guaranteed v-prefix
# Note: this is probably not consumed in our build chain.
# `VERSION` above is expected to have a `v` prefix, which is
# then automatically stripped in places that must not have it
# (e.g., in context of Helm).
vVERSION := v$(VERSION:v%=%)

GOLANG_VERSION := $(shell ./hack/golang-version.sh)
TOOLKIT_CONTAINER_IMAGE := $(shell ./hack/toolkit-container-image.sh)

# These variables are only needed when building a local image
BUILDIMAGE_TAG ?= devel-go$(GOLANG_VERSION)
BUILDIMAGE ?=  $(DRIVER_NAME):$(BUILDIMAGE_TAG)

GIT_COMMIT ?= $(shell git describe --match="" --dirty --long --always --abbrev=40 2> /dev/null || echo "")

print-%:
	@echo $($*)
