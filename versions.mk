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

REGISTRY ?= nvcr.io/nvidia/cloud-native

VERSION  ?= v0.1.0

# vVERSION represents the version with a guaranteed v-prefix
vVERSION := v$(VERSION:v%=%)

GOLANG_VERSION ?= 1.23.1
CUDA_VERSION ?= 12.3.2

# These variables are only needed when building a local image
CLIENT_GEN_VERSION ?= v0.29.2
LISTER_GEN_VERSION ?= v0.29.2
INFORMER_GEN_VERSION ?= v0.29.2
CONTROLLER_GEN_VERSION ?= v0.17.1
GOLANGCI_LINT_VERSION ?= v1.52.0
MOQ_VERSION ?= v0.4.0

BUILDIMAGE_TAG ?= devel-go$(GOLANG_VERSION)
BUILDIMAGE ?=  ghcr.io/nvidia/k8s-test-infra:$(BUILDIMAGE_TAG)

GIT_COMMIT ?= $(shell git describe --match="" --dirty --long --always --abbrev=40 2> /dev/null || echo "")
