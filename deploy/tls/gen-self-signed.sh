#!/usr/bin/env bash
# Generate a self-signed TLS cert/key for the kite-kvm API.
# For production, bring your own certificate (internal CA or Let's Encrypt).
#
# Usage: gen-self-signed.sh [OUT_DIR]   (default /etc/kite-kvm/tls)
set -euo pipefail

OUT_DIR="${1:-/etc/kite-kvm/tls}"
CN="${KITE_TLS_CN:-$(hostname -f 2>/dev/null || hostname)}"
DAYS="${KITE_TLS_DAYS:-3650}"

# Build the SAN list: the hostname, localhost, and every host IPv4.
SANS="DNS:${CN},DNS:localhost,IP:127.0.0.1"
for ip in $(hostname -I 2>/dev/null || true); do
  SANS="${SANS},IP:${ip}"
done

mkdir -p "$OUT_DIR"
openssl req -x509 -newkey rsa:4096 -nodes \
  -keyout "$OUT_DIR/server.key" -out "$OUT_DIR/server.crt" \
  -days "$DAYS" -subj "/CN=${CN}" \
  -addext "subjectAltName=${SANS}"

chmod 600 "$OUT_DIR/server.key"
chmod 644 "$OUT_DIR/server.crt"
if id kite-kvm >/dev/null 2>&1; then
  chown kite-kvm:kite-kvm "$OUT_DIR/server.key" "$OUT_DIR/server.crt" 2>/dev/null || true
fi

echo "Wrote $OUT_DIR/server.crt and server.key"
echo "SANs: ${SANS}"
echo "NOTE: self-signed — API clients must trust it (curl -k in dev)."
