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

# Cloud Build / Prow: package the Helm chart and push it to
# oci://${IMG_PREFIX}/charts. Chart semver is IMG_TAG with a leading "v" removed.

set -euo pipefail

REPO_ROOT=$(git rev-parse --show-toplevel)
cd "$REPO_ROOT"

if [[ -z ${IMG_PREFIX:-} ]]; then
	echo "IMG_PREFIX is not set" >&2
	exit 1
fi

if [[ -z ${IMG_TAG:-} ]]; then
	if git describe --exact-match --tags HEAD >/dev/null 2>&1; then
		IMG_TAG=$(git describe --exact-match --tags HEAD)
	else
		IMG_TAG=$(make --no-print-directory -f "${REPO_ROOT}/versions.mk" print-VERSION_W_COMMIT)
	fi
fi
echo "Using IMG_TAG=${IMG_TAG}"

CHART_VERSION="${IMG_TAG#v}"
echo "Using CHART_VERSION=${CHART_VERSION} (IMG_TAG without leading v)"

DRIVER_NAME=$(make --no-print-directory -f "${REPO_ROOT}/versions.mk" print-DRIVER_NAME)
HELM="${HELM:-helm}"
DIST_DIR="${REPO_ROOT}/dist"

if ! command -v helm >/dev/null 2>&1; then
	echo "Installing Helm 3..."
	curl -sSfLO --retry 8 --retry-all-errors --connect-timeout 10 --retry-delay 5 \
		https://get.helm.sh/helm-v3.18.6-linux-amd64.tar.gz
	tar -zxvf helm-v3*linux-amd64.tar.gz
	mv linux-amd64/helm /usr/local/bin/helm
fi

mkdir -p "${DIST_DIR}"
rm -f "${DIST_DIR}/${DRIVER_NAME}-"*.tgz

# Staging image registry swap for non-release builds only. Tagged (release) builds
# keep registry.k8s.io/dra-driver-nvidia in values.yaml for promoted charts.
VALUES="${REPO_ROOT}/helm/${DRIVER_NAME}/values.yaml"
if git describe --exact-match --tags HEAD >/dev/null 2>&1; then
	echo "Tagged release build: skipping staging registry rewrite in values.yaml"
else
	sed -i 's|registry.k8s.io/dra-driver-nvidia|us-central1-docker.pkg.dev/k8s-staging-images/dra-driver-nvidia|g' "${VALUES}"
	git diff || echo "ignore git diff exit code"
fi

"${HELM}" package "helm/${DRIVER_NAME}" \
	--version "${CHART_VERSION}" \
	--app-version "${CHART_VERSION}" \
	--destination "${DIST_DIR}"

CHART_TGZ="${DIST_DIR}/${DRIVER_NAME}-${CHART_VERSION}.tgz"
echo "Pushing ${CHART_TGZ} -> oci://${IMG_PREFIX}/charts"
"${HELM}" push "${CHART_TGZ}" "oci://${IMG_PREFIX}/charts"
