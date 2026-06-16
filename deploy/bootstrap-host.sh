#!/usr/bin/env bash
# Bootstrap the libvirt host prerequisites for kite-kvm: the storage pool, the
# NAT 'default' network, and the base-image directory. Idempotent. Run as a user
# that can reach qemu:///system (root, or a member of the libvirt group).
set -euo pipefail

VIRSH="virsh -c qemu:///system"
POOL="${KITE_POOL:-default}"
IMAGES_DIR="${KITE_IMAGES_DIR:-/var/lib/libvirt/images}"
BASE_DIR="${KITE_BASE_DIR:-/var/lib/libvirt/images/base}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> storage pool '$POOL' -> $IMAGES_DIR"
if ! $VIRSH pool-info "$POOL" >/dev/null 2>&1; then
  $VIRSH pool-define-as "$POOL" dir --target "$IMAGES_DIR"
  $VIRSH pool-build "$POOL" || true
fi
$VIRSH pool-start "$POOL" 2>/dev/null || true
$VIRSH pool-autostart "$POOL"

echo "==> NAT network 'default'"
if ! $VIRSH net-info default >/dev/null 2>&1; then
  $VIRSH net-define "$SCRIPT_DIR/networks/nat-default.xml"
fi
$VIRSH net-start default 2>/dev/null || true
$VIRSH net-autostart default

echo "==> base-image directory $BASE_DIR"
mkdir -p "$BASE_DIR"

echo
echo "Done. Verify with:"
echo "  $VIRSH pool-list --all"
echo "  $VIRSH net-list --all"
echo "For public-IP (bridge) mode, see deploy/networks/README.md."
