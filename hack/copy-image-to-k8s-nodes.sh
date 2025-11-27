#!/usr/bin/env bash
set -o errexit
set -o nounset
set -o pipefail

if [ $# -ne 1 ]; then
    echo "first arg: required: image ref, example: nvcr.io/nvidia/k8s-dra-driver-gpu:v25.12.0-dev"
    exit 1
else
    export CONT_IMAGE_REF="$1"
fi

username="${USER}"
IMG_TAR_FILE_PATH="$(mktemp ./dra-driver-dev-img.XXXXXXX.tar.gz)"
trap 'rm -f "$IMG_TAR_FILE_PATH"' EXIT

# Get all internal IPs of the currently configured k8s nodes,
# one line per IP.
IPs=$(
  kubectl get nodes -o \
  jsonpath='{range .items[*]}{.status.addresses[?(@.type=="InternalIP")].address}{"\n"}{end}'
  )

echo -e "internal IPs of k8s nodes:\n${IPs}"


copy_to_import_on() {
  local ip="${1}"

  # Note(JP): I tweaked this to save time -- each SSH session establishment here
  # creates noticeable overhead -- try to all required steps in a single SSH
  # session, in a streaming-like fashion. Rely on password-less sudo on the
  # remote machine. Transport gzipped tarball over SSH, decompress the stream on
  # the remote end and feed right away into ctr's import command (which expects
  # an uncompressed tarball on stdin).
  /usr/bin/time -f "copy/import($ip) took: %e s" \
    bash -c \
      "cat ${IMG_TAR_FILE_PATH} | ssh "${username}@${ip}" \
        'cat | gunzip | sudo --prompt=\"\" -S -- ctr -n k8s.io images import -'" \
          2>&1 | grep -v DEPRECATION
}


# Fast compression; adds ~no time, but in a measured example reduced image size
# from ~250 MB to ~70 MB, which shaves off a bit over a second doing SCP over a
# common network.
echo "export image ..."
/usr/bin/time -f "export/compress took: %e s" \
  docker save "${CONT_IMAGE_REF}" | pigz > "${IMG_TAR_FILE_PATH}"
set +x

ls -lh -- "${IMG_TAR_FILE_PATH}"

echo "push image to nodes ..."
for ip in ${IPs}; do
  # Do that in parallel for all target nodes.
  copy_to_import_on "${ip}" &
  sleep 0.1
done
wait

echo "done"
