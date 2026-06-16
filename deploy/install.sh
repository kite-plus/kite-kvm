#!/usr/bin/env bash
# Idempotent installer for kite-kvm on a Linux KVM host. Run as root.
# Installs the binary, config, TLS cert, systemd unit, the kite-kvm user, and
# bootstraps the libvirt host prerequisites.
set -euo pipefail

[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_SRC="${KITE_BIN:-$REPO_DIR/bin/kite-kvm-linux-amd64}"

echo "==> user kite-kvm (member of libvirt group)"
id kite-kvm >/dev/null 2>&1 || useradd -r -g libvirt -s /usr/sbin/nologin kite-kvm
usermod -aG libvirt kite-kvm

echo "==> binary -> /usr/local/bin/kite-kvm"
[ -f "$BIN_SRC" ] || { echo "binary $BIN_SRC missing — run 'make build-linux' first" >&2; exit 1; }
install -m 0755 "$BIN_SRC" /usr/local/bin/kite-kvm

echo "==> config -> /etc/kite-kvm/kite-kvm.yaml"
install -d -m 0755 /etc/kite-kvm
if [ ! -f /etc/kite-kvm/kite-kvm.yaml ]; then
  install -m 0640 -o kite-kvm -g kite-kvm "$REPO_DIR/configs/kite-kvm.example.yaml" /etc/kite-kvm/kite-kvm.yaml
  echo "   (edit it and set auth.tokens before starting)"
fi

echo "==> TLS cert (self-signed if absent)"
[ -f /etc/kite-kvm/tls/server.crt ] || "$SCRIPT_DIR/tls/gen-self-signed.sh" /etc/kite-kvm/tls

echo "==> host prerequisites (storage pool + NAT network)"
"$SCRIPT_DIR/bootstrap-host.sh"

echo "==> systemd unit"
install -m 0644 "$SCRIPT_DIR/kite-kvm.service" /etc/systemd/system/kite-kvm.service
systemctl daemon-reload
systemctl enable kite-kvm

cat <<'NEXT'

Installed. Next steps:
  1) Edit /etc/kite-kvm/kite-kvm.yaml  -> set auth.tokens (and review networks/flavors/images)
  2) ./deploy/fetch-images.sh          -> download golden cloud images
  3) systemctl start kite-kvm
  4) journalctl -u kite-kvm -f         -> watch logs
  5) curl -k https://127.0.0.1:8443/readyz
NEXT
