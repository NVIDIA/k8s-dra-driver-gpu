#!/bin/bash

set -o nounset
set -o errexit

SPECPATH="$1"
FAULT_TYPE="$2"

BASE_NAME="test-failover-job"
CD_NAME="test-failover-cd"

# External supervisor can inject run ID (for many-repetition-tests), used mainly
# in output file names.
RUNID="${RUNID:-no_runid}"

JOB_NAME="${BASE_NAME}-launcher"

# For measuring duration with sub-second precision.
_T0=$(awk '{print $1}' /proc/uptime)

# For measuring duration with O(1 s) precision.
SECONDS=0

# Common arguments for `kubectl logs`, with common ts for proper chronological
# sort upon dedup/post-processing.
KLOGS_ARGS="--tail=-1 --prefix --all-containers --timestamps"

# Wait for the workload to heal after fault injection (for the MPI launcher pod
# to succeed); otherwise fail the test TIMEOUT seconds after startup.
TIMEOUT=300

log_ts_no_newline() {
    echo -n "$(date -u +'%Y-%m-%dT%H:%M:%S.%3NZ ')"
}

log() {
  _TNOW=$(awk '{print $1}' /proc/uptime)
  _DUR=$(echo "$_TNOW - $_T0" | bc)
  log_ts_no_newline
  printf "[%6.1fs] $1\n" "$_DUR"
}

log "RUNID $RUNID | fault type $FAULT_TYPE | $SPECPATH | $BASE_NAME | $JOB_NAME | $CD_NAME"
log "do: delete -f ${SPECPATH} (and wait)"
kubectl delete -f "${SPECPATH}" --ignore-not-found > /dev/null
kubectl wait --for=delete job/"${JOB_NAME}" --timeout=20s > /dev/null
log "done"

log "do: apply -f ${SPECPATH}"
kubectl apply -f "${SPECPATH}" > /dev/null
log "done"
log "do: wait --for=create"
kubectl wait --for=create job/"${JOB_NAME}" --timeout=40s > /dev/null
log "done"
CDUID=$(kubectl describe computedomains.resource.nvidia.com "${CD_NAME}" | grep UID | awk '{print $2}')

log "CD uid: ${CDUID}"
log "resource claims:"
kubectl get resourceclaim
log "workload pods:"
kubectl get pods -o wide


LAUNCHER_LOG_PATH="${RUNID}_launcher_logs.log"
LAUNCHER_ERRORS_LOG_PATH="${RUNID}_launcher_errors.log"
echo "" > "${LAUNCHER_LOG_PATH}"
echo "" > "${LAUNCHER_LOG_PATH}".dup

FAULT_INJECTED=0
NVB_COMMS_STARTED=0
LAST_LAUNCHER_RESTART_OUTPUT=""
STATUS="nil"

while true; do

    _llro=$(kubectl get pod -l job-name="${JOB_NAME}" -o json | \
        /usr/bin/jq -r '.items[].status.containerStatuses[].restartCount'
    )

    if [[ "$LAST_LAUNCHER_RESTART_OUTPUT" != "$_llro" ]]; then
        log "launcher container restarts seen: $_llro"
        LAST_LAUNCHER_RESTART_OUTPUT="$_llro"
    fi

    # Start log-follower child processes for all newly popping up CD daemon pods
    # (when they are Running). I have added this very late in the game because I
    # think we're missing CD daemon log around container shutdown; I want to be
    # extra sure.
    kubectl get pods -n nvidia-dra-driver-gpu | grep "${CD_NAME}" | grep Running | awk '{print $1}' | while read pname; do
        _logfname="${RUNID}_cddaemon_follow_${pname}.log"
        if [ -f "$_logfname" ]; then
            continue
        fi
        log "new CD daemon pod: $pname -- follow log, save to ${_logfname}"
        kubectl logs -n nvidia-dra-driver-gpu "$pname" \
            --tail=-1 --timestamps --prefix --all-containers --follow \
            > "${_logfname}" &
        # Note: if we lose track of the log followers spawned, we can and should
        # terminate them all with `kill $(jobs -p)`.
    done

    # Note that the launcher _pod_ is not expected to restart. The container in
    # the pod may restart various times in the context of this failover.
    # `kubectl logs --follow` does not automatically follow container restarts.
    # To catch all container instances in view of quick restarts, we need to
    # often call a pair of `kubectl logs` commands (once with, and once without
    # --previous). Even that does not reliably obtain _all_ container logs. The
    # correct solution for this type of problem is to have a proper log
    # streaming pipeline. Collect heavily duplicated logs (dedup later)
    kubectl logs -l job-name="${JOB_NAME}" $KLOGS_ARGS >> "${LAUNCHER_LOG_PATH}".dup 2>&1 || true
    kubectl logs -l job-name="${JOB_NAME}" $KLOGS_ARGS --previous --ignore-not-found >> "${LAUNCHER_LOG_PATH}".dup 2>&1 || true


    date -u +'%Y-%m-%dT%H:%M:%S.%3NZ ' >> "${RUNID}_pods_over_time"
    kubectl get pods -n nvidia-dra-driver-gpu -o wide >> "${RUNID}_pods_over_time"
    kubectl get pods -o wide >> "${RUNID}_pods_over_time"

    STATUS=$(kubectl get pod -l job-name="${JOB_NAME}" -o jsonpath="{.items[0].status.phase}" 2>/dev/null)
    if [ "$STATUS" == "Succeeded" ]; then
        log "nvb completed"
        break
    fi

    # The launcher pod handles many failures internally by restarting the
    # launcher container (the MPI launcher process). Treat it as permanent
    # failure when this pod failed overall.
    if [ "$STATUS" == "Failed" ]; then
        log "nvb launcher pod failed"
        break
    fi

    # Keep rather precise track of when the actual communication part of the
    # benchmark has started. Assume that the benchmark takes at least 20 seconds
    # overall. Inject fault shortly after benchmark has started. Pick that delay
    # to be random (but below 20 seconds).
    if (( NVB_COMMS_STARTED == 1 )); then
        if (( FAULT_INJECTED == 0 )); then
            log "NVB_COMMS_STARTED"

            _jitter_seconds=$(awk -v min=1 -v max=5 'BEGIN {srand(); print min+rand()*(max-min)}')
            log "sleep, pre-injection jitter: $_jitter_seconds s"
            sleep "$_jitter_seconds"

            # MPI error handling between launcher and worker: here, and MPI
            # worker process tries to actively propagate any error that _it_
            # experiences (such as a failing CUDA mem import/export API call in
            # a worker process) to the launcher. When that error propagation
            # works (via TCP), the launcher container attempts to log error
            # detail and then terminates itself. The launcher pod then restarts
            # the launcher container. After that, the MPI workload (the nvb
            # benchmark in this case, running across the workers) is
            # re-initialized by the new launcher. It starts again from scratch
            # (while the MPI SSH-type worker processes stay alive -- they
            # actually just start new workload child processes). Another type of
            # failure that that launcher handles by restarting itself is clean
            # TCP connection shutdown initiated by (clean) worker pod deletion.
            # Any type of failure that is propagated to the launcher is met with
            # the launcher container crashing, and restarting worker processes.
            # This type of error handling after all facilitates "healing" the
            # workload.
            #
            # Here, however, the workload _never_ proceeds from where it left
            # off before fault injection. When this test passes, it implies that
            # the workload restarted from scratch internally after fault
            # injection, and then completed.

            if (( FAULT_TYPE == 1 )); then
                log "inject fault type 1: force-delete worker pod 0"
                set -x
                kubectl delete pod "${BASE_NAME}-worker-0" --grace-period=0 --force
                set +x
            elif (( FAULT_TYPE == 2 )); then
                log "inject fault type 2: force-delete all IMEX daemons"
                set -x
                kubectl delete pod -n nvidia-dra-driver-gpu -l resource.nvidia.com/computeDomain --grace-period=0 --force
                set +x
            elif (( FAULT_TYPE == 3 )); then
                log "inject fault type 3: regular-delete worker pod 1"
                set -x
                kubectl delete pod "${BASE_NAME}-worker-1"
                set +x
            else
                log "unknown fault type $FAULT_TYPE"
                exit 1
            fi
            FAULT_INJECTED=1
        fi
        # Fault already injected, do not inject again.
    else
        # Did the benchmark start? Consult current launcher container log.
        if kubectl logs -l job-name="${JOB_NAME}" --tail=-1 2>&1 | grep "Running multinode_"; then
            NVB_COMMS_STARTED=1
        fi
    fi

    if [ "$SECONDS" -ge $TIMEOUT ]; then
        log "global deadline reached ($TIMEOUT seconds), collect debug data -- and leave control loop"
        kubectl get pods -A -o wide
        kubectl get computedomain
        kubectl get computedomains.resource.nvidia.com "${CD_NAME}" -o yaml

        # Run this in the background, then delete workflow -- this helps getting all logs
        # (but also disrupts post-run debuggability)
        kubectl logs -l "training.kubeflow.org/job-name=${BASE_NAME}" \
            --tail=-1 --prefix --all-containers --timestamps --follow &> "${RUNID}_on_timeout_workload.log" &
        log "on-timeout do: delete -f ${SPECPATH} (and wait)"
        kubectl delete -f "${SPECPATH}" --ignore-not-found > /dev/null
        kubectl wait --for=delete job/"${JOB_NAME}" --timeout=20s > /dev/null

        # log something if this looks like a segmentation fault on
        # shutdown (not our bug)
        set +e
        cat "${RUNID}_on_timeout_workload.log" | grep PMIx_Finalize
        set -e

        log "done"
        break
    fi

    sleep 0.5
done


log "terminate children, wait"
jobs -p
kill $(jobs -p) 2> /dev/null || true
wait

log "dedup launcher logs"
cat "${LAUNCHER_LOG_PATH}".dup | sort | uniq > "${LAUNCHER_LOG_PATH}"
rm "${LAUNCHER_LOG_PATH}".dup

set +e
log "errors in / reported by launcher:"
cat "${LAUNCHER_LOG_PATH}" | \
    grep -e CUDA_ -e "closed by remote host" -e "Could not resolve" > "${LAUNCHER_ERRORS_LOG_PATH}"
cat "${LAUNCHER_ERRORS_LOG_PATH}"

if [ "$STATUS" != "Succeeded" ]; then
    log "last launcher pod status is not 'Succeeded': $STATUS"
    log "finished: failure (fault type $FAULT_TYPE)"
    log "exit with code 1"
    exit 1
fi

log "finished: success (fault type $FAULT_TYPE)"