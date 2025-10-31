#!/bin/bash

# Usage: ./unbind_from_driver.sh <ssss:bb:dd.f>
# Unbind the GPU specified by the PCI_ID=ssss:bb:dd.f from the driver its bound to.
# Attempt to acquire the unbindLock within the retries specified before unbinding the device from its driver.

acquire_unbind_lock()
{
   local gpu=$1
   local lock_retries=5
   local unbind_lock_file="/proc/driver/nvidia/gpus/$gpu/unbindLock"
   local unbind_lock=0
   local attempt=1

   if [ ! -e "${unbind_lock_file}" ]; then
      return 0
   fi

   while [[ $attempt -le ${lock_retries} ]]; do
      echo "[retry $attempt/${lock_retries}] Attempting to acquire unbindLock for $gpu" >&1

      echo 1 > "{$unbind_lock_file}"
      read -r unbind_lock < "${unbind_lock_file}"
      if [ ${unbind_lock} -eq 1 ]; then
         echo "UnbindLock acquired for $gpu" >&1
         return 0
      fi

      sleep $attempt
      attempt=$((attempt + 1))
   done

   echo "cannot obtain unbindLock for $gpu" >&2
   return 1
}

unbind_from_driver()
{
   local gpu=$1
   local existing_driver
   local existing_driver_name

   [ -e "/sys/bus/pci/devices/$gpu/driver" ] || return 0
   existing_driver=$(readlink -f "/sys/bus/pci/devices/$gpu/driver")
   existing_driver_name=$(basename "${existing_driver}")
   if [ "${existing_driver_name}" == "nvidia" ]; then
      acquire_unbind_lock "$gpu" || return 1
   fi
   echo "$gpu" > "$existing_driver/unbind"
   return 0
}

unbind_from_driver "$1" || exit 1