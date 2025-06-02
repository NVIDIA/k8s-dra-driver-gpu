#!/bin/bash

CURRENT_DIR="$(cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd)"
PROJECT_DIR="$(cd -- "$( dirname -- "${CURRENT_DIR}/../../../.." )" &> /dev/null && pwd)"

set -ex
set -o pipefail

# We extract information from versions.mk
function from_versions_mk() {
    local makevar=$1
    local value=$(grep -E "^\s*${makevar}\s+[\?:]= " ${PROJECT_DIR}/versions.mk)
    echo ${value##*= }
}
DRIVER_NAME=$(from_versions_mk "DRIVER_NAME")

: "${CLUSTER_NAME:=${DRIVER_NAME}-cluster}"
: "${AWS_REGION:=us-east-1}"

export CLUSTER_NAME
export AWS_REGION

# Delete EKS cluster using eksctl
eksctl delete cluster --name ${CLUSTER_NAME} --region ${AWS_REGION} --wait