#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

script_dir=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd -P ) ; readonly script_dir
repo_dir=$(git rev-parse --show-toplevel) ; readonly repo_dir

cd "${repo_dir}"

set -x
git apply "${script_dir}/000-network-policies.patch"
{ set +x; } 2>/dev/null
