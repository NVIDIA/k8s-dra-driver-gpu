<!--
SPDX-FileCopyrightText: Copyright (c) 2025 NVIDIA CORPORATION & AFFILIATES. All rights reserved.
SPDX-License-Identifier: Apache-2.0

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

# NVIDIA DRA GPU Driver E2E Test Suite

This directory contains the end-to-end (E2E) test suite for the [NVIDIA Kubernetes DRA GPU Driver](https://github.com/NVIDIA/k8s-dra-driver-gpu). It is implemented using [Ginkgo v2](https://onsi.github.io/ginkgo/) and [Gomega](https://onsi.github.io/gomega/) and validates driver deployment and functionality in a real Kubernetes cluster.

## Requirements

- Kubernetes cluster (v1.26+ recommended)
- Configured `KUBECONFIG` pointing to target cluster
- Go 1.21+
- Required environment variables (see below)

## Environment Variables

| Variable                   | Required | Description                                              |
|----------------------------|----------|----------------------------------------------------------|
| `KUBECONFIG`               | ✅       | Path to kubeconfig file                                  |
| `HELM_CHART`               | ✅       | Path or name of the Helm chart for the DRA driver        |
| `E2E_IMAGE_REPO`           | ✅       | Container image repository for the driver                |
| `E2E_IMAGE_TAG`            | ✅       | Image tag for the driver                                 |
| `E2E_IMAGE_PULL_POLICY`    | ✅       | Pull policy for the image (e.g., `IfNotPresent`)         |
| `E2E_TIMEOUT_SECONDS`      | ❌       | Timeout for test suite in seconds (default: 1800)        |
| `E2E_HOST_MANAGED_DRIVERS` | ❌       | Whether host manages the NVIDIA driver (default: true)   |
| `LOG_ARTIFACTS_DIR`        | ❌       | Path to store logs and diagnostics (default: ./e2e_logs) |
| `COLLECT_LOGS_FROM`        | ❌       | Namespace or component to collect logs from              |

## Makefile Targets

The project includes a `Makefile` with the following targets:

```Makefile
GINKGO_ARGS ?=
LOG_ARTIFACTS_DIR ?= $(CURDIR)/e2e_logs

ginkgo:
	mkdir -p $(CURDIR)/bin
	GOBIN=$(CURDIR)/bin go install github.com/onsi/ginkgo/v2/ginkgo@latest

test-e2e: ginkgo
	$(CURDIR)/bin/ginkgo $(GINKGO_ARGS) -v --json-report $(CURDIR)/ginkgo.json ./tests/e2e/...
```

## Running the Tests

You can run the test suite using:

```bash
make test-e2e
```

Or manually:

```bash
make ginkgo
./bin/ginkgo -v ./tests/e2e
```

## Notes

- Each test runs in an isolated namespace and cleans up automatically.
- Node labels `nvidia.com/gpu.present=true` are applied for scheduling.
- Helm values can be overridden for container-managed drivers.

## License

Apache 2.0. See [LICENSE](../LICENSE).
