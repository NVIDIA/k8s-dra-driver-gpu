#!/usr/bin/env bash

# Main intent: help users to self-troubleshoot when the GPU driver is not set up
# properly before installing this DRA driver. In that case, the log of the init
# container running this script is meant to yield an actionable error message.
# For now, rely on k8s to implement a high-level retry with back-off.

if [ -z "$NVIDIA_DRIVER_ROOT" ]; then
    # Not set, or set to empty string (not distinguishable).
    # Normalize to "/" (treated as such elsewhere).
    export NVIDIA_DRIVER_ROOT="/"
fi

echo "NVIDIA_DRIVER_ROOT set to: $NVIDIA_DRIVER_ROOT"

# This may be slow or hang, in a bad setup.
echo "run: chroot /driver-root nvidia-smi"

# chroot passes on the exit code of the invoked command.
chroot /driver-root nvidia-smi
RCODE="$?"

# For checking GPU driver health: for now, rely on nvidia-smi's exit code.
# Rely on code 0 meaning that the driver is properly set up. For example,
# code 9 means that the GPU driver is not loaded; see section 'RETURN VALUE'
# in the man page.
if [ ${RCODE} -eq 0 ]
then
    echo "chrooted nvidia-smi returned with code 0: success, leave"
    exit 0
fi

printf '%b' \
"nvidia-smi failed (see error above). " \
"Has the NVIDIA GPU driver been set up? " \
"The GPU driver is expected to be installed under " \
"NVIDIA_DRIVER_ROOT (currently set to '${NVIDIA_DRIVER_ROOT}') " \
"in the host filesystem. If that path appears to be unexpected: " \
"review and adjust the 'nvidiaDriverRoot' Helm chart variable. " \
"If the value is expected: review if the GPU driver has " \
"actually been installed under NVIDIA_DRIVER_ROOT.\n"

if [ "${NVIDIA_DRIVER_ROOT}" == "/run/nvidia/driver" ]; then
    printf '%b' \
    "Hint: you may want the NVIDIA GPU Operator to manage the GPU driver " \
    "(NVIDIA_DRIVER_ROOT is set to /run/nvidia/driver): " \
    "make sure that Operator is deployed and healthy.\n"
fi

# Specific, common mistake.
if   [ "${NVIDIA_DRIVER_ROOT}" == "/" ] && [ -f /driver-root/run/nvidia/driver/usr/bin/nvidia-smi ]; then
    printf '%b' \
    "Hint: /run/nvidia/driver/usr/bin/nvidia-smi exists on the host, you " \
    "may want to re-install the DRA driver Helm chart with " \
    "--set nvidiaDriverRoot=/run/nvidia/driver\n"
    exit 1
fi

# Rely on k8s hostPath type `Directory` for this mount (this directory actually
# existed on the host).
if [ -z "$( ls -A /driver-root )" ]; then
    echo "Hint: Directory $NVIDIA_DRIVER_ROOT on the host appears to be empty"
    exit 1
fi

# Seen in the wild: orphaned and incomplete /driver-root with contents from a
# previous operator-provided driver setup.
if [ ! -f "/driver-root/usr/bin/nvidia-smi" ]; then
    echo "Hint: Directory $NVIDIA_DRIVER_ROOT not empty, but does not seem to contain GPU driver binaries"
fi
exit 1
