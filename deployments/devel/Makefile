# Copyright 2024 NVIDIA CORPORATION
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



install-tools: cmd-tools | tools-go

tools-go:
	@echo Installing tools from tools.go
	@cat tools.go | grep _ | awk -F'"' '{print $$2}' | xargs -tI % go install %

# Defining the following tools through tools.go is unstable and thus we define
# their versions explicitly.
CLIENT_GEN_VERSION ?= v0.34.0
LISTER_GEN_VERSION ?= v0.34.0
INFORMER_GEN_VERSION ?= v0.34.0
CONTROLLER_GEN_VERSION ?= v0.19.0
cmd-tools:
	@go install sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN_VERSION}
	@go install k8s.io/code-generator/cmd/client-gen@${CLIENT_GEN_VERSION}
	@go install k8s.io/code-generator/cmd/lister-gen@${LISTER_GEN_VERSION}
	@go install k8s.io/code-generator/cmd/informer-gen@${INFORMER_GEN_VERSION}

DOCKER ?= docker
-include $(CURDIR)/versions.mk

DOCKERFILE_DEVEL = deployments/devel/Dockerfile
DOCKERFILE_CONTEXT = deployments/devel

.PHONY: .build-image
.build-image:
	$(DOCKER) build \
		--progress=plain \
		--build-arg GOLANG_VERSION=$(GOLANG_VERSION) \
		--build-arg BASH_STATIC_GIT_REF="$(BASH_STATIC_GIT_REF)" \
		--build-arg TOOLKIT_CONTAINER_IMAGE="$(TOOLKIT_CONTAINER_IMAGE)" \
		--tag $(BUILDIMAGE) \
		-f $(DOCKERFILE_DEVEL) \
		$(DOCKERFILE_CONTEXT)

modules:
	go mod tidy
	go mod verify

check-modules: modules
	git diff --quiet HEAD -- go.mod go.sum
