# kite-kvm

**English** | [简体中文](README.zh-CN.md)

A single-binary **KVM control node** (被控节点) that runs alongside `libvirtd` on a
Linux host, manages local virtual machines through libvirt, and exposes an
authenticated REST API so any billing system (WHMCS, IDCSmart, a custom panel, …)
can **provision, bill, and operate VPS** instances. The API is
billing-system-agnostic — it's infrastructure; integrations live downstream.

## Design highlights

- **Single binary, no cgo** — uses the pure-Go [`digitalocean/go-libvirt`](https://github.com/digitalocean/go-libvirt)
  (RPC over the libvirt socket), so `CGO_ENABLED=0 GOOS=linux` cross-compiles a
  static Linux binary straight from macOS with no C toolchain.
- **Same-host deployment** — connects to `qemu:///system` over the local unix
  socket; libvirtd is never exposed on the network.
- **Reconciliation-grade control plane** — async jobs + idempotency keys + SQLite
  persistence, so a billing system's retries and concurrent provisioning never
  double-allocate.
- **Two networking modes** — NAT (port-forwarding) and bridged public IP,
  selectable per VM.
- **Testable anywhere** — all libvirt calls sit behind one `Conn` interface, so
  the entire pipeline is unit-testable on macOS via an in-memory fake.

## Features

- VM lifecycle: create, list, get, terminate (full teardown).
- Power: start, graceful shutdown, reboot, force stop.
- Billing verbs: suspend, unsuspend, password reset.
- Reconfigure: change hostname, rebuild from image, resize / change package.
- Browser VNC console: single-use token + websocket proxy to the VM's VNC port.
- Snapshots: create, list, delete, and revert (system checkpoints).
- Traffic quota: per-VM combined in+out transfer cap with automatic full network
  cutoff (NIC link down) on overage, plus manual block/unblock and period reset.
- Live resource stats (CPU / memory / network / block) with interval rates.
- Provisioning: thin qcow2 overlay off a golden cloud image + cloud-init NoCloud
  seed (hostname, users, password, SSH keys, network), built in pure Go.
- Networking: NAT with pinned DHCP leases, or a host bridge with a public IP pool;
  optional per-NIC bandwidth cap.
- Auth: TLS + bearer token + IP allowlist.

## Quick start (local dev on macOS)

Without libvirt, set `libvirt.uri` to `fake://` to exercise the whole flow against
an in-memory implementation:

```yaml
# configs/dev.yaml
server: {addr: "127.0.0.1:8443", insecure: true}
auth: {tokens: ["devtoken"]}
libvirt: {uri: "fake://", instance_dir: "/tmp/kite-instances"}
storage: {state_path: "/tmp/kite.db"}
networks: [{id: nat-default, mode: nat, default: true, libvirt_network: default, subnet: "192.168.122.0/24"}]
flavors: [{id: s1.small, name: Small, vcpus: 1, memory_mb: 1024, disk_gb: 20}]
images:  [{id: ubuntu-22.04, name: Ubuntu 22.04, base_path: /tmp/base.img, default_user: ubuntu}]
```

```bash
go run ./cmd/kite-kvm -config configs/dev.yaml

curl -k -H "Authorization: Bearer devtoken" \
  -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"flavor_id":"s1.small","image_id":"ubuntu-22.04","hostname":"web1"}' \
  https://127.0.0.1:8443/v1/vms
```

## Build

Requires Go 1.25+.

```bash
make build        # host binary -> bin/kite-kvm
make build-linux  # static, cgo-free Linux binary for deployment
make test         # unit tests
```

## Deploy (Linux host)

The control node runs on a Linux host with `libvirtd` / KVM and access to the
libvirt socket (typically by joining the `libvirt` group).

Prerequisites:

- A libvirt storage pool (default `default`, a directory pool under
  `/var/lib/libvirt/images`).
- Read-only golden images in `libvirt.image_base_dir` (e.g. Ubuntu/Debian cloud
  images, which ship cloud-init and virtio).
- NAT: the default `default`/virbr0 network. Bridge: a pre-configured host bridge
  (e.g. `br0`) plus a public IP pool.
- TLS certificate/key and a bearer token for the API.

```bash
make build-linux
sudo install bin/kite-kvm-linux-amd64 /usr/local/bin/kite-kvm
sudo install -D configs/kite-kvm.example.yaml /etc/kite-kvm/kite-kvm.yaml   # edit
sudo install -D deploy/kite-kvm.service /etc/systemd/system/kite-kvm.service
sudo useradd -r -g libvirt kite-kvm
sudo systemctl enable --now kite-kvm
```

## API

See [docs/api.md](docs/api.md) for the full reference. All endpoints are under
`/v1`, require a bearer token (and pass the IP allowlist), and run over TLS.
Mutating operations are asynchronous: they return `202` with a job, require an
`Idempotency-Key`, and are polled via `GET /v1/jobs/{id}`.

## Roadmap

Implemented: VM CRUD, power operations, suspend/unsuspend, password reset,
hostname change, rebuild, resize, browser VNC console, snapshots, live stats,
NAT and bridged public IP, async jobs + idempotency + SQLite persistence.

Planned: an OpenAPI spec as the integration contract, snapshot export/backup to a
file, secondary IP / DNAT port forwarding, Prometheus metrics, multi-host
scheduling, and an LVM storage pool. (Billing-system adapters — WHMCS, IDCSmart,
custom — are downstream consumers and live in their own repos, not here.)
