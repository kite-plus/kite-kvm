# kite-kvm

**English** | [简体中文](README.zh-CN.md)

A single-binary KVM control node (被控节点). It runs alongside `libvirtd` on a
Linux host and exposes an authenticated REST API to provision and operate VPS
instances. The API is billing-system-agnostic — WHMCS, IDCSmart, or a custom
panel integrate against it from their own repos.

- **No cgo** — pure-Go [`go-libvirt`](https://github.com/digitalocean/go-libvirt) +
  [`libvirtxml`](https://libvirt.org/go/libvirtxml), so `CGO_ENABLED=0 GOOS=linux`
  cross-compiles a static binary straight from macOS.
- **Reconciliation-grade** — async jobs, idempotency keys, SQLite state, startup
  reconciliation, and bounded retries, so a billing system's retries and
  concurrent provisioning never double-allocate.
- **Self-contained** — talks to `qemu:///system` over the local socket;
  provisions via a qcow2 overlay + cloud-init seed; both NAT and bridged
  public-IP networking are built in.
- **Testable anywhere** — all libvirt calls sit behind one `Conn` interface, so
  the full pipeline runs on macOS against an in-memory fake.

## Features

VM CRUD · power ops · suspend/unsuspend · password/hostname/rebuild/resize ·
browser VNC console · snapshots · per-VM traffic quota with automatic network
cutoff · live stats · host capacity & admission control · TLS + bearer token +
IP allowlist.

## Requirements

- Go 1.25+ to build.
- A Linux host with `libvirtd` / KVM to run (the agent joins the `libvirt` group).

## Quick start (no libvirt, any OS)

Set `libvirt.uri: fake://` to exercise the whole flow against an in-memory
hypervisor:

```yaml
# configs/dev.yaml
server:   {addr: "127.0.0.1:8443", insecure: true}
auth:     {tokens: ["devtoken"]}
libvirt:  {uri: "fake://", instance_dir: "/tmp/kite"}
storage:  {state_path: "/tmp/kite.db"}
networks: [{id: nat, mode: nat, default: true, libvirt_network: default}]
flavors:  [{id: s1.small, name: Small, vcpus: 1, memory_mb: 1024, disk_gb: 20}]
images:   [{id: ubuntu-22.04, name: Ubuntu, base_path: /tmp/base.img, default_user: ubuntu}]
```

```bash
go run ./cmd/kite-kvm -config configs/dev.yaml

curl -k -H "Authorization: Bearer devtoken" -H "Idempotency-Key: $(uuidgen)" \
  -d '{"flavor_id":"s1.small","image_id":"ubuntu-22.04","hostname":"web1"}' \
  https://127.0.0.1:8443/v1/vms
```

## Build

```bash
make build         # host binary -> bin/kite-kvm
make build-linux   # static, cgo-free Linux binary
make test
```

## Deploy

```bash
make build-linux
sudo ./deploy/install.sh   # binary, config, TLS cert, systemd unit, user, host bootstrap
```

`install.sh` is idempotent. See [docs/deploy.md](docs/deploy.md) for the full
guide (TLS, bridge networking, backup, upgrade).

## Documentation

- API: [docs/api.md](docs/api.md) · OpenAPI [docs/openapi.yaml](docs/openapi.yaml) · rendered [docs/api.html](docs/api.html)
- Deploy & ops: [docs/deploy.md](docs/deploy.md)

All `/v1` endpoints take a bearer token over TLS. Mutating operations are
asynchronous: `202` + a job, an `Idempotency-Key` header, polled via
`GET /v1/jobs/{id}`.

## Roadmap

Snapshot export/backup to a file · secondary IP / DNAT port forwarding ·
Prometheus metrics · multi-host scheduling · LVM storage pool.
