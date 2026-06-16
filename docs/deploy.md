# Deploying kite-kvm

kite-kvm runs on a Linux KVM host alongside `libvirtd`. It is a single static
binary plus a config file and a SQLite state file.

## 1. Prerequisites

- A Linux host with KVM + `libvirtd` running, and the `virsh`, `qemu-img`, and
  `openssl` tools.
- A libvirt storage pool (dir type) and the NAT `default` network — created by
  `deploy/bootstrap-host.sh`.
- Golden cloud images (Ubuntu/Debian) in the base-image dir — fetched by
  `deploy/fetch-images.sh`.
- For public-IP (bridge) mode: a host bridge — see
  [deploy/networks/README.md](../deploy/networks/README.md).

## 2. Build

From a dev machine (no cgo, cross-compiles from macOS):

```bash
make build-linux        # -> bin/kite-kvm-linux-amd64
```

## 3. Install

Copy the repo (or at least `bin/`, `deploy/`, `configs/`) to the host and run:

```bash
sudo ./deploy/install.sh
```

This installs the binary, config, a self-signed TLS cert, the systemd unit, the
`kite-kvm` user (in the `libvirt` group), and bootstraps the pool + NAT network.
It is idempotent.

Then:

```bash
sudoedit /etc/kite-kvm/kite-kvm.yaml      # set auth.tokens, review networks/flavors/images
sudo ./deploy/fetch-images.sh             # download golden images
sudo systemctl start kite-kvm
journalctl -u kite-kvm -f
curl -k https://127.0.0.1:8443/readyz
```

## 4. TLS

The API requires TLS (unless `server.insecure: true`, dev only).

- **Self-signed** (testing/internal): `deploy/tls/gen-self-signed.sh` (run by the
  installer). Clients must trust it (`curl -k` in dev).
- **Bring your own**: point `server.tls_cert` / `server.tls_key` at your PEM
  files (internal CA, or Let's Encrypt via certbot). Reload after renewals.

## 5. The kite-kvm / libvirt / qemu user relationship

- The agent runs as `kite-kvm`, a member of `libvirt`, so it can reach
  `qemu:///system` without root.
- qemu opens disks and the cloud-init seed as `libvirt-qemu`. Overlays go through
  the libvirt storage API (correct owner); the seed ISO is written `0644` so qemu
  can read it. Keep `instance_dir` group-accessible.

## 6. Operations

- **Logs**: structured JSON to stdout → `journalctl -u kite-kvm`.
- **Health**: `/healthz` (liveness), `/readyz` (libvirt reachable). Unauthenticated.
- **State backup**: `/var/lib/kite-kvm/state.db` is the only durable record of
  the VM↔domain mapping, IP allocations, and traffic usage. Back it up (it is
  SQLite WAL; copy with the service stopped, or use `sqlite3 .backup`). Losing it
  orphans VMs from their billing identities.

## 7. Upgrade / rollback

The binary is stateless (all state is in SQLite + libvirt):

```bash
sudo systemctl stop kite-kvm
sudo install -m 0755 bin/kite-kvm-linux-amd64 /usr/local/bin/kite-kvm
sudo systemctl start kite-kvm
```

Rollback = install the previous binary the same way. Schema migrations are
forward-only and applied automatically on start; back up `state.db` before a
major upgrade.

## 8. Capacity / admission

Set the optional `capacity` block in the config to make the host refuse creates
it cannot satisfy (memory/vCPU overcommit ratios, reserved memory, max VMs).
`GET /v1/host` reports live capacity and commitments for a scheduler/panel.
