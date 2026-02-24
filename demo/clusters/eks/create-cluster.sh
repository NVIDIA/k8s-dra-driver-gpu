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

: "${AWS_REGION:=us-east-1}"
: "${CLUSTER_NAME:=${DRIVER_NAME}-cluster}"
: "${EKS_VERSION:=1.33}"
: "${INSTANCE_TYPE:=g6e.4xlarge}"

export AWS_REGION
export CLUSTER_NAME
export EKS_VERSION
export INSTANCE_TYPE

export EKS_CP_AZS=$(aws ec2 describe-availability-zones \
      --region ${AWS_REGION} \
      --filters "Name=opt-in-status,Values=opt-in-not-required" \
      --query "AvailabilityZones[?ZoneId!='use1-az3'].[ZoneName][:3]" \
      --output text | sed 's/ /, /g; s/^/  - /')

## Create eksctl configuration file
envsubst < eksctl.yaml > ${CLUSTER_NAME}-${AWS_REGION}.yaml

## Create EKS cluster using eksctl
eksctl create cluster -f ${CLUSTER_NAME}-${AWS_REGION}.yaml --install-nvidia-plugin=false

## Setup EKS cluster credentials
aws eks update-kubeconfig --name ${CLUSTER_NAME} --region ${AWS_REGION}