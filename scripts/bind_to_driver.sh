#!/bin/bash

# Usage: ./bind_to_driver.sh <ssss:bb:dd.f> <driver>
# Bind the GPU specified by the PCI_ID=ssss:bb:dd.f to the given driver.

bind_to_driver()
{
   local gpu=$1
   local driver=$2
   local drivers_path="/sys/bus/pci/drivers"
   local driver_override_file="/sys/bus/pci/devices/$gpu/driver_override"
   local bind_file="$drivers_path/$driver/bind"

   if [ ! -e "$driver_override_file" ]; then
      echo "'$driver_override_file' file does not exist" >&2
      return 1
   fi

   echo "$driver" > "$driver_override_file"
   if [ $? -ne 0 ]; then
      echo "failed to write '$driver' to $driver_override_file" >&2
      return 1
   fi

   if [ ! -e "$bind_file" ]; then
      echo "'$bind_file' file does not exist" >&2
      return 1
   fi

   echo "$gpu" > "$bind_file"
   if [ $? -ne 0 ]; then
      echo "failed to write '$gpu' to $bind_file" >&2
      echo "" > "$driver_override_file"
      return 1
   fi
}

bind_to_driver "$1" "$2" || exit 1