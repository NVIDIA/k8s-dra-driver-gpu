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
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
cd "$REPO_ROOT"

if [[ -z ${IMG_PREFIX:-} ]]; then
	echo "IMG_PREFIX is not set"
	exit 1
fi

if [[ -z ${IMG_TAG:-} ]]; then
	# Use a tag if the current commit is a tag, otherwise use a date+git-hash tag
	if git describe --exact-match --tags HEAD >/dev/null 2>&1; then
		IMG_TAG=$(git describe --exact-match --tags HEAD)
	else
		IMG_TAG="$(date +v%Y%m%d)-$(git rev-parse --short HEAD)"
	fi
fi
echo "Using IMG_TAG=${IMG_TAG}"

make -f deployments/container/Makefile build \
	IMAGE_NAME="${IMG_PREFIX}/nvidia-dra-driver-gpu" \
	IMAGE_TAG="${IMG_TAG}" \
	BUILD_MULTI_ARCH_IMAGES="${BUILD_MULTI_ARCH_IMAGES:-true}" \
	PUSH_ON_BUILD="${PUSH_ON_BUILD:-true}" \
	CI="${CI:-true}"
