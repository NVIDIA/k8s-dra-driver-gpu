# Copyright (c) 2020-2022, NVIDIA CORPORATION.  All rights reserved.
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

DOCKER   ?= docker
MKDIR    ?= mkdir
TR       ?= tr
CC       ?= cc
DIST_DIR ?= $(CURDIR)/dist

include $(CURDIR)/common.mk
include $(CURDIR)/versions.mk

.NOTPARALLEL:

ifeq ($(IMAGE_NAME),)
IMAGE_NAME = $(REGISTRY)/$(DRIVER_NAME)
endif

CMDS := $(patsubst ./cmd/%/,%,$(sort $(dir $(wildcard ./cmd/*/))))
CMD_TARGETS := $(patsubst %,cmd-%, $(CMDS))

CHECK_TARGETS := golangci-lint
MAKE_TARGETS := binaries build build-image check fmt lint-internal test examples cmds coverage generate vendor check-modules $(CHECK_TARGETS)

TARGETS := $(MAKE_TARGETS) $(CMD_TARGETS)

DOCKER_TARGETS := $(patsubst %,docker-%, $(TARGETS))
.PHONY: $(TARGETS) $(DOCKER_TARGETS)

GOOS ?= linux
GOARCH ?= $(shell uname -m | sed -e 's,aarch64,arm64,' -e 's,x86_64,amd64,')
ifeq ($(VERSION),)
CLI_VERSION = $(LIB_VERSION)$(if $(LIB_TAG),-$(LIB_TAG))
else
CLI_VERSION = $(VERSION)
endif
CLI_VERSION_PACKAGE = $(MODULE)/internal/info

binaries: cmds
ifneq ($(PREFIX),)
cmd-%: COMMAND_BUILD_OPTIONS = -o $(PREFIX)/$(*)
endif
cmds: $(CMD_TARGETS)
$(CMD_TARGETS): cmd-%:
	CGO_LDFLAGS_ALLOW='-Wl,--unresolved-symbols=ignore-in-object-files' \
		CC=$(CC) CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -ldflags "-s -w -X $(CLI_VERSION_PACKAGE).gitCommit=$(GIT_COMMIT) -X $(CLI_VERSION_PACKAGE).version=$(CLI_VERSION)" $(COMMAND_BUILD_OPTIONS) $(MODULE)/cmd/$(*)

build:
	CC=$(CC) GOOS=$(GOOS) GOARCH=$(GOARCH) go build ./...

examples: $(EXAMPLE_TARGETS)
$(EXAMPLE_TARGETS): example-%:
	CC=$(CC) GOOS=$(GOOS) GOARCH=$(GOARCH) go build ./examples/$(*)

all: check test build binaries
check: $(CHECK_TARGETS)

# Apply go fmt to the codebase
fmt:
	go list -f '{{.Dir}}' $(MODULE)/... \
		| xargs gofmt -s -l -w

## goimports: Apply goimports -local to the codebase
goimports:
	find . -name \*.go \
			-not -name "zz_generated.deepcopy.go" \
			-not -path "./vendor/*" \
			-not -path "./pkg/nvidia.com/resource/clientset/versioned/*" \
		-exec goimports -local $(MODULE) -w {} \;

golangci-lint:
	golangci-lint run ./...

vendor:
	go mod tidy
	go mod vendor
	go mod verify

check-modules: vendor
	git diff --quiet HEAD -- go.mod go.sum vendor

COVERAGE_FILE := coverage.out
test: build cmds
	go test -race -cover -v -coverprofile=$(COVERAGE_FILE) $(MODULE)/...

coverage: test
	cat $(COVERAGE_FILE) | grep -v "_mock.go" > $(COVERAGE_FILE).no-mocks
	go tool cover -func=$(COVERAGE_FILE).no-mocks

generate: generate-deepcopy fmt

generate-deepcopy: .remove-deepcopy
	for dir in $(DEEPCOPY_SOURCES); do \
		controller-gen \
			object:headerFile=$(CURDIR)/hack/boilerplate.go.txt,year=$(shell date +"%Y") \
			paths=$(CURDIR)/$${dir}/ \
			output:object:dir=$(CURDIR)/$${dir}; \
	done

.remove-deepcopy:
	for dir in $(DEEPCOPY_SOURCES); do \
		rm -f $(CURDIR)/$${dir}/zz_generated.deepcopy.go; \
	done

# Generate an image for containerized builds
# Note: This image is local only
.PHONY: .build-image
.build-image:
	make -f deployments/devel/Makefile .build-image

ifeq ($(BUILD_DEVEL_IMAGE),yes)
$(DOCKER_TARGETS): .build-image
.shell: .build-image
endif

$(DOCKER_TARGETS): docker-%:
	@echo "Running 'make $(*)' in container image $(BUILDIMAGE)"
	$(DOCKER) run \
		--rm \
		-e GOCACHE=/tmp/.cache/go \
		-e GOMODCACHE=/tmp/.cache/gomod \
		-e GOLANGCI_LINT_CACHE=/tmp/.cache/golangci-lint \
		-v $(PWD):/work \
		-w /work \
		--user $$(id -u):$$(id -g) \
		$(BUILDIMAGE) \
			make $(*)

# Start an interactive shell using the development image.
PHONY: .shell
.shell:
	$(DOCKER) run \
		--rm \
		-ti \
		-e GOCACHE=/tmp/.cache/go \
		-e GOMODCACHE=/tmp/.cache/gomod \
		-e GOLANGCI_LINT_CACHE=/tmp/.cache/golangci-lint \
		-v $(PWD):/work \
		-w /work \
		--user $$(id -u):$$(id -g) \
		$(BUILDIMAGE)
