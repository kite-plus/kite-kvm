# kite-kvm REST API

> Machine-readable contract: [openapi.yaml](openapi.yaml) (OpenAPI 3.1) — use it to
> generate clients or import into Swagger UI / Postman. A rendered, browsable
> version is [api.html](api.html) (open in a browser; regenerate with `make docs`).
> This page is the prose companion.

All endpoints are under `/v1` and require:

- `Authorization: Bearer <token>` — one of the tokens in `auth.tokens`.
- A source IP allowed by `auth.ip_allowlist` (if configured).
- TLS, unless the server runs with `server.insecure: true` (dev only).

Health probes (`/healthz`, `/readyz`) are unauthenticated.

## Conventions

- **Mutating** operations (create, power, suspend, password, terminate) are
  asynchronous. They return **`202 Accepted`** with a job, a `Location` header
  pointing at the job, and require an **`Idempotency-Key`** header. Poll
  `GET /v1/jobs/{id}` for completion.
- **Read** operations are synchronous and return `200`.
- Errors use the envelope `{"error": {"code": "...", "message": "..."}}`.

Async response body:

```json
{ "job_id": "9f...e3", "status": "queued", "vm_id": "2e...c9" }
```

## Health

| Method | Path | Description |
|---|---|---|
| GET | `/healthz` | Liveness — process is up. |
| GET | `/readyz` | Readiness — libvirt is reachable. |

## Catalog

| Method | Path | Description |
|---|---|---|
| GET | `/v1/flavors` | List provisionable flavors. |
| GET | `/v1/images` | List base images. |

## VM lifecycle

### Create — `POST /v1/vms`

Headers: `Idempotency-Key: <unique>`.

```json
{
  "flavor_id": "s1.small",
  "image_id": "ubuntu-22.04",
  "hostname": "web1",
  "password": "s3cret",
  "ssh_keys": ["ssh-ed25519 AAAA... user@host"],
  "network": { "mode": "nat" }
}
```

`network` selection precedence: `network_id` → `mode` (nat|bridge) default of that
mode → the configured default network. Returns `202` + job.

Billing action: provision.

### List / Get / Status / Stats

| Method | Path | Description |
|---|---|---|
| GET | `/v1/vms` | List VMs (`{"vms": [...]}`). |
| GET | `/v1/vms/{id}` | VM details. Power state is reconciled live. |
| GET | `/v1/vms/{id}/status` | `{id, status, power_state}`. |
| GET | `/v1/vms/{id}/stats` | Live CPU/mem/net/block counters + interval rates. |

VM `status`: `provisioning | running | stopped | suspended | error | terminated`.
`power_state`: `running | shutoff | paused | unknown`.

### Terminate — `DELETE /v1/vms/{id}`

Headers: `Idempotency-Key`. Destroys and undefines the domain, deletes the
overlay disk and seed, releases the IP, and marks the VM terminated. Idempotent.

Billing action: terminate.

## Power operations

All take `Idempotency-Key` and return `202` + job.

| Method | Path | Description |
|---|---|---|
| POST | `/v1/vms/{id}/start` | Start a stopped VM. |
| POST | `/v1/vms/{id}/shutdown` | Graceful ACPI shutdown. |
| POST | `/v1/vms/{id}/reboot` | Graceful reboot. |
| POST | `/v1/vms/{id}/stop` | Force power-off. |

## Billing / reconfigure

The "billing action" column names the neutral lifecycle operation this endpoint
serves, so any billing system (WHMCS, IDCSmart, a custom panel, …) can map its
own verbs onto it. The API itself is billing-system-agnostic.

| Method | Path | Description | Billing action |
|---|---|---|---|
| POST | `/v1/vms/{id}/suspend` | Stop + mark suspended (records prior power state). | suspend |
| POST | `/v1/vms/{id}/unsuspend` | Restore prior power state. | unsuspend |
| POST | `/v1/vms/{id}/password` | Reset password (`{"password": "..."}`). Applies on next boot. | — |
| POST | `/v1/vms/{id}/hostname` | Change hostname (`{"hostname": "..."}`). Applies on next boot. | — |
| POST | `/v1/vms/{id}/rebuild` | Reinstall from an image (`{"image_id"?, "password"?, "ssh_keys"?}`). Recreates the disk; data is lost. | reinstall |
| POST | `/v1/vms/{id}/resize` | Change package (`{"flavor_id": "..."}`). Disk is grow-only; causes a brief reboot. | change package |

## Console (VNC)

| Method | Path | Auth | Description |
|---|---|---|---|
| POST | `/v1/vms/{id}/console` | bearer | Mint a single-use console token (VM must be running). |
| GET | `/console/ws/{token}` | token only | WebSocket that proxies the VM's VNC (RFB) stream. |

`POST /v1/vms/{id}/console` returns:

```json
{
  "token": "B_oKTD9u...",
  "websocket_path": "/console/ws/B_oKTD9u...",
  "expires_at": "2026-06-15T17:08:22Z"
}
```

A browser noVNC client connects to `wss://<agent-host>/console/ws/{token}`. The
token is single-use and expires after 60s. The websocket endpoint is
authenticated solely by this token (browsers cannot send a bearer header), so it
sits outside the bearer/allowlist middleware; the VM's VNC stays bound to
`127.0.0.1` and is only reachable through this proxy.

## Host

| Method | Path | Description |
|---|---|---|
| GET | `/v1/host` | Host capacity + commitments: CPU cores, total/free memory, storage, and the vCPU/RAM/disk committed to non-terminated VMs. For schedulers/panels. |

Create is admission-controlled when `capacity` limits are configured: a request
that would exceed the host's memory/vCPU/VM caps is rejected with `409`.

## Snapshots

| Method | Path | Description |
|---|---|---|
| GET | `/v1/vms/{id}/snapshots` | List snapshots (`{"snapshots": [{name, state, creation_time, current}]}`). |
| POST | `/v1/vms/{id}/snapshots` | Create a snapshot (`{"name"?, "description"?}`; name auto-generated if omitted). |
| DELETE | `/v1/vms/{id}/snapshots/{snap}` | Delete a snapshot. |
| POST | `/v1/vms/{id}/snapshots/{snap}/revert` | Revert the VM to a snapshot. |

Create/delete/revert are async (`202` + job, `Idempotency-Key` required). A
running VM's snapshot includes memory state (a system checkpoint).

## Traffic quota

Per-VM combined in+out transfer quota. A background poller samples usage every
`traffic.interval_seconds`; when a VM crosses its quota its NIC link is cut
(full network cutoff) and restored when it drops back under (e.g. after a reset
or quota raise). The flavor sets the default quota; `traffic_quota_gb` on create
or `PUT .../traffic` overrides it per VM (0 = unlimited).

| Method | Path | Description |
|---|---|---|
| GET | `/v1/vms/{id}/traffic` | Usage view: `{quota_bytes, used_bytes, unlimited, percent, period_start, blocked, block_reason}`. |
| PUT | `/v1/vms/{id}/traffic` | Set this VM's quota (`{"quota_gb": 2048}`; 0 = unlimited). Synchronous. |
| POST | `/v1/vms/{id}/traffic/reset` | Zero usage, start a new period, restore network if quota-blocked. |
| POST | `/v1/vms/{id}/traffic/block` | Manually cut the VM's network. |
| POST | `/v1/vms/{id}/traffic/unblock` | Manually restore the VM's network. |

reset/block/unblock are async (`202` + job, `Idempotency-Key` required).

## Jobs

| Method | Path | Description |
|---|---|---|
| GET | `/v1/jobs/{id}` | Poll job state: `queued | running | succeeded | failed` (+ `error`). |

## Status codes

| Code | Meaning |
|---|---|
| 200 | OK (reads). |
| 202 | Accepted (async mutation enqueued). |
| 400 | Bad request (missing `Idempotency-Key`, invalid JSON). |
| 401 | Missing/invalid bearer token. |
| 403 | Source IP not allowed. |
| 404 | VM/job not found. |
| 409 | Conflict (idempotency-key reuse, terminated VM, no IP available). |
| 422 | Unprocessable (unknown flavor/image/network). |
| 501 | Reserved capability not implemented. |
