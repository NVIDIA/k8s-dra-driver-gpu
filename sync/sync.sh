#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

dir=$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd ) ; readonly dir
cd "${dir}/.."

# Stage 1 sync
set -x
vendir sync
{ set +x; } 2>/dev/null

# Remove trailing whitespace at end of lines (hack to fix vendir bug).
# Uses `-i.bak` + cleanup for BSD/GNU sed portability.
find upstream/ -type f -exec sed -i.bak 's/[[:space:]]*$//' {} \;
find upstream/ -type f -name '*.bak' -delete

# Patches
./sync/patches/team-label/patch.sh
./sync/patches/network-policies/patch.sh
./sync/patches/kyverno-policies/patch.sh

# Store diffs between vendored upstream and shipped chart for review.
rm -rf ./diffs
mkdir -p ./diffs

upstream_chart="upstream/k8s-dra-driver-gpu/deployments/helm/dra-driver-nvidia-gpu"
shipped_chart="deployments/helm/dra-driver-nvidia-gpu"

for f in $(git --no-pager diff --no-exit-code --no-color --no-index "${upstream_chart}" "${shipped_chart}" --name-only) ; do
    [[ "$f" == "/dev/null" ]] && continue
    # Chart.yaml is owned by the fork (GS version / appVersion).
    [[ "$f" == "${shipped_chart}/Chart.yaml" ]] && continue

    base_file="${upstream_chart}/${f#"${shipped_chart}/"}"
    [[ ! -e $base_file ]] && base_file="/dev/null"

    set +e
    set -x
    git --no-pager diff --no-exit-code --no-color --no-index "$base_file" "${f}" \
            > "./diffs/${f//\//__}.patch" # ${f//\//__} replaces all "/" with "__"
    { set +x; } 2>/dev/null
    ret=$?
    set -e
    if [ $ret -ne 0 ] && [ $ret -ne 1 ] ; then
            exit $ret
    fi
done
