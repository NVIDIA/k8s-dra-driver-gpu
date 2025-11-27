#  SPDX-FileCopyrightText: Copyright (c) 2025 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
#  SPDX-License-Identifier: Apache-2.0
#
#  Licensed under the Apache License, Version 2.0 (the "License");
#  you may not use this file except in compliance with the License.
#  You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
#  Unless required by applicable law or agreed to in writing, software
#  distributed under the License is distributed on an "AS IS" BASIS,
#  WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
#  See the License for the specific language governing permissions and
#  limitations under the License.

include $(CURDIR)/versions.mk
include $(CURDIR)/common.mk

# The to-be-tested Helm chart. Ignored when setting TEST_CHART_LOCAL.
TEST_CHART_REPO ?= "oci://ghcr.io/nvidia/k8s-dra-driver-gpu"
TEST_CHART_VERSION ?= "$(VERSION_GHCR_CHART)"

# The baseline Helm chart to test upgrades from and downgrades to.
TEST_CHART_LASTSTABLE_REPO ?= "oci://ghcr.io/nvidia/k8s-dra-driver-gpu"
TEST_CHART_LASTSTABLE_VERSION ?= "25.3.2-2c250af3-chart"

# If not "false": the to-be-tested Helm chart is installed from the local
# filesystem (from `deployments/helm/nvidia-dra-driver-gpu`). Make sure
# (out-of-band) that the container images that the chart refers to are available
# (placed directly on the involved nodes or pullable). This is a convenince
# parameter for setting
#
#        TEST_CHART_REPO="deployments/helm/nvidia-dra-driver-gpu/"
#        TEST_CHART_VERSION="$(make print-VERSION)" (but w/o v prefix)
#
TEST_CHART_LOCAL ?= "false"

# Consumed in upgrade test via kubectl apply -f <URL>
# (can be a branch, tag, or commit). TODO: parse default
# from `TEST_CHART_VERSION`.
TEST_CRD_UPGRADE_TARGET_GIT_REF ?= "main"

TEST_NVIDIA_DRIVER_ROOT ?= "/run/nvidia/driver"

# TODO: calculate precise, expected container image spec,
# and test for that in test suite, for example:
# `nvcr.io/nvidia/k8s-dra-driver-gpu:v25.8.0-dev`
TEST_EXPECTED_IMAGE_SPEC_SUBSTRING ?= $(VERSION)

ifneq ($(TEST_CHART_LOCAL),"false")
TEST_CHART_REPO = "deployments/helm/nvidia-dra-driver-gpu"
TEST_CHART_VERSION = $(VERSION:v%=%)
endif

BATS_IMAGE ?= batstests:$(GIT_COMMIT_SHORT)
BATS_ARGS ?= --print-output-on-failure --no-tempdir-cleanup --timing --abort
KUBECONFIG ?= $(HOME)/.kube/config
RUNDIR_PARENT ?= /tmp/k8s-dra-driver-gpu-tests-out-$(USER)

# Add `docker run` arguments when not running
# in Github Actions / GitLab CI.
DOCKER_RUN_FLAGS :=
ifeq ($(CI),)
  DOCKER_RUN_FLAGS += -it
endif

DOCKER_ENVS = \
	--env KUBECONFIG=$(KUBECONFIG) \
	--env TEST_CHART_REPO=$(TEST_CHART_REPO) \
	--env TEST_CHART_VERSION=$(TEST_CHART_VERSION) \
	--env TEST_CHART_LASTSTABLE_REPO=$(TEST_CHART_LASTSTABLE_REPO) \
	--env TEST_CHART_LASTSTABLE_VERSION=$(TEST_CHART_LASTSTABLE_VERSION) \
	--env TEST_CRD_UPGRADE_TARGET_GIT_REF=$(TEST_CRD_UPGRADE_TARGET_GIT_REF) \
	--env TEST_NVIDIA_DRIVER_ROOT=$(TEST_NVIDIA_DRIVER_ROOT) \
	--env TEST_EXPECTED_IMAGE_SPEC_SUBSTRING=$(TEST_EXPECTED_IMAGE_SPEC_SUBSTRING)

DOCKER_UID := $(shell id -u)
DOCKER_GID := $(shell id -g)
DOCKER_USER := $(DOCKER_UID):$(DOCKER_GID)

DOCKER_MOUNTS = \
	-v /tmp:/tmp \
	-v $(CURDIR):/cwd \
	-v $(dir $(abspath $(KUBECONFIG))):$(dir $(abspath $(KUBECONFIG)))

# Helper to run bats inside container
# $(1) = whitespace-separated list of test files
define RUN_BATS
	@export _RUNDIR_PARENT="$(RUNDIR_PARENT)"; \
	mkdir -p "$${_RUNDIR_PARENT}"; \
	export _RUNDIR="$$(mktemp -p "$${_RUNDIR_PARENT}" -d -t bats-tests-$$(date +%s)-XXXXX)"; \
	echo "Run dir: $${_RUNDIR}"; \
	docker run --rm $(DOCKER_RUN_FLAGS) $(DOCKER_MOUNTS) $(DOCKER_ENVS) \
	  -u $(DOCKER_USER) --entrypoint /bin/bash $(BATS_IMAGE) \
	  -c "set -ex; cd /cwd; \
	      echo 'Running k8s cluster cleanup (invasive)...'; \
	      bash tests/bats/cleanup-from-previous-run.sh 2>&1 | tee -a $${_RUNDIR}/cleanup.outerr; \
	      set +x; echo '--- STARTING TEST SUITE ---'; set -x; \
	      TMPDIR="$${_RUNDIR}" bats $(BATS_ARGS) $(1) \
      "
endef

default: tests

.PHONY: runner-image
runner-image:
	docker buildx build . -t $(BATS_IMAGE) -f tests/bats/Dockerfile

# Warning: destructive against currently configured k8s cluster.
#
# Explicit invocation of 'cleanup-from-previous-run.sh' (could also be done as
# suite/file 'setup' in bats, but we'd lose output on success). During dev, you
# may want to add --show-output-of-passing-tests (and read bats docs for other
# cmdline args).
.PHONY: tests-gpu tests-cd

# Run GPU plugin specific tests
tests-gpu: runner-image
	$(call RUN_BATS, \
		tests/bats/test_basics.bats \
		tests/bats/test_gpu_basic.bats \
		tests/bats/test_gpu_stress.bats)

# Run Compute Domain specific tests
tests-cd: runner-image
	$(call RUN_BATS, \
		tests/bats/test_basics.bats \
		tests/bats/test_cd_imex_chan_inject.bats \
		tests/bats/test_cd_mnnvl_workload.bats \
		tests/bats/test_cd_misc.bats \
		tests/bats/test_cd_logging.bats \
		tests/bats/test_cd_failover.bats \
		tests/bats/test_cd_updowngrade.bats)

# Run complete tests
tests: image
	$(call RUN_BATS, \
		tests/bats/test_basics.bats \
		tests/bats/test_gpu_basic.bats \
		tests/bats/test_cd_imex_chan_inject.bats \
		tests/bats/test_cd_mnnvl_workload.bats \
		tests/bats/test_cd_misc.bats \
		tests/bats/test_cd_logging.bats \
		tests/bats/test_cd_failover.bats \
		tests/bats/test_cd_updowngrade.bats \
		tests/bats/test_gpu_stress.bats)
