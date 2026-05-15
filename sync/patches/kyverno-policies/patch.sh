#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

script_dir=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd -P ) ; readonly script_dir
repo_dir=$(git rev-parse --show-toplevel) ; readonly repo_dir

cd "${repo_dir}"

# This file is not present upstream; vendir sync removes it on each run.
# Copy it back into the chart's templates.
set -x
cp "${script_dir}/kyverno-policy-exception.yaml" \
   "${repo_dir}/deployments/helm/dra-driver-nvidia-gpu/templates/kyverno-policy-exception.yaml"
{ set +x; } 2>/dev/null
