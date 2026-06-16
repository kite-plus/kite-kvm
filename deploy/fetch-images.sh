#!/usr/bin/env bash
# Download golden cloud images into the kite-kvm base-image directory. These are
# the read-only bases each VM's overlay is created from. They ship cloud-init
# and virtio, which kite-kvm requires. Idempotent (skips existing files).
set -euo pipefail

BASE_DIR="${KITE_BASE_DIR:-/var/lib/libvirt/images/base}"
mkdir -p "$BASE_DIR"

# name -> URL. Match these paths to image.base_path in your config.
declare -A IMAGES=(
  ["jammy-server-cloudimg-amd64.img"]="https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img"
  ["debian-12-genericcloud-amd64.qcow2"]="https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2"
)

for name in "${!IMAGES[@]}"; do
  dest="$BASE_DIR/$name"
  if [ -f "$dest" ]; then
    echo "exists: $dest"
    continue
  fi
  echo "downloading $name ..."
  curl -fSL "${IMAGES[$name]}" -o "$dest.part"
  mv "$dest.part" "$dest"
  chmod 644 "$dest"
done

# qemu (libvirt-qemu) must be able to read the bases.
if id libvirt-qemu >/dev/null 2>&1; then
  chown libvirt-qemu "$BASE_DIR"/* 2>/dev/null || true
fi

echo
echo "Base images in $BASE_DIR:"
ls -lh "$BASE_DIR"
