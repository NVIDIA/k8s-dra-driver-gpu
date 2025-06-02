#!/bin/bash

PROJECT_DIR="$(cd -- "$( dirname -- "${CURRENT_DIR}/../../../.." )" &> /dev/null && pwd)"

helm upgrade -i --create-namespace --namespace nvidia-dra-driver-gpu nvidia-dra-driver-gpu ${PROJECT_DIR}/deployments/helm/nvidia-dra-driver-gpu \
  --set image.pullPolicy=Always \
  --set gpuResourcesEnabledOverride=true \
  --set controller.tolerations\[0\].key="CriticalAddonsOnly" \
  --set controller.tolerations\[0\].operator=Exists \
  --set controller.tolerations\[0\].effect=NoSchedule \
  --set controller.nodeSelector.role=system \
  --set controller.affinity=null \
  --set kubeletPlugin.tolerations\[0\].key="nvidia.com/gpu" \
  --set kubeletPlugin.tolerations\[0\].operator=Exists \
  --set kubeletPlugin.tolerations\[0\].effect=NoSchedule