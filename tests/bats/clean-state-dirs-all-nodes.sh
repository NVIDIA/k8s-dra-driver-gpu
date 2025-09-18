#!/bin/bash
#
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

set -o errexit
set -o nounset
set -o pipefail

rm_kubelet_plugin_dirs_from_node () {
    local NODE_NAME="$1"
    echo "Run privileged pod to remove kubelet plugin directories on node ${NODE_NAME}"
    kubectl run privpod-rm-plugindirs \
        --rm \
        --image=busybox \
        --attach \
        --wait \
        --restart=Never \
        --overrides='{
        "spec": {
            "nodeName": "'"${NODE_NAME}"'",
            "containers": [{
            "name": "privpod-rm-plugindirs",
            "image": "busybox",
            "securityContext": { "privileged": true },
            "volumeMounts": [{
                "mountPath": "/host",
                "name": "host-root"
            }],
            "command": ["/bin/sh", "-c", "rm -rfv /host/var/lib/kubelet/plugins/*"]
            }],
            "volumes": [{
            "name": "host-root",
            "hostPath": { "path": "/" }
            }]
        }
        }'
}

# Would be faster when using a daemonset. However, the output is more readable
# when running sequentially.
for node in $(kubectl get nodes -o jsonpath='{.items[*].metadata.name}'); do
    rm_kubelet_plugin_dirs_from_node $node
done
