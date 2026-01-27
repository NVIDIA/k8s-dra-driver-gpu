#!/bin/bash

source tests/bats/helpers.sh

export N="$1"
yq -i -y "select(.kind == \"ComputeDomain\").spec.numNodes = $N" demo/specs/imex/channel-injection.yaml
cat demo/specs/imex/channel-injection.yaml | grep numNodes

make image-build-and-copy-to-nodes

helm uninstall -n nvidia-dra-driver-gpu nvidia-dra-driver-gpu --wait
helm install nvidia-dra-driver-gpu deployments/helm/nvidia-dra-driver-gpu/ \
    --create-namespace \
    --namespace nvidia-dra-driver-gpu \
    --set resources.gpus.enabled=false \
    --set nvidiaDriverRoot=/run/nvidia/driver \
    --set featureGates.IMEXDaemonsWithDNSNames=true \
    --set logVerbosity=6 \
    --wait


kubectl delete -f demo/specs/imex/channel-injection.yaml --ignore-not-found=true
sleep 2

kubectl apply -f demo/specs/imex/channel-injection.yaml
#kubectl get pods -A -o wide | grep dra | grep imex
date -u +%Y-%m-%dT%H:%M:%S%z

read start_time _ < /proc/uptime
i=0
while true; do
    ((i++))
    output=$(kubectl get deployments.apps -A | grep imex-channel-injection)

    if [[ $output == *"$N/$N"* ]]; then
        echo "done"
        echo $output
        break
    fi

    if (( i % 5 == 0 )); then
        echo "$output"
    fi
    sleep 0.5
done

read end_time _ < /proc/uptime
duration=$(echo "$end_time - $start_time" | bc)

echo "matching pod count dropped to zero"

kubectl get computedomains.resource.nvidia.com imex-channel-injection -o json | \
    jq -r '.status.nodes[].cliqueID' | \
    sort | \
    uniq -c

echo
echo "Total time: $duration seconds"

get_all_cd_daemon_logs_for_cd_name imex-channel-injection | \
    grep PATCH | \
    grep 'computedomains' | \
    grep '200 OK' | \
    grep -oP 'milliseconds=\K\d+' | \
    uplot hist --nbins 12

get_all_cd_daemon_logs_for_cd_name imex-channel-injection | \
    grep -oP 't_process_start \K[0-9.]+' | \
    uplot hist --nbins 12