#!/usr/bin/env bash

set -ex
set -o pipefail

oc patch --type merge -p '{"spec":{"profile": "HighNodeUtilization", "profileCustomizations": {"dynamicResourceAllocation": "Enabled"}}}' scheduler cluster
