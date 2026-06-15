# kite-kvm REST API

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

WHMCS mapping: `CreateAccount`.

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

WHMCS mapping: `TerminateAccount`.

## Power operations

All take `Idempotency-Key` and return `202` + job.

| Method | Path | Description |
|---|---|---|
| POST | `/v1/vms/{id}/start` | Start a stopped VM. |
| POST | `/v1/vms/{id}/shutdown` | Graceful ACPI shutdown. |
| POST | `/v1/vms/{id}/reboot` | Graceful reboot. |
| POST | `/v1/vms/{id}/stop` | Force power-off. |

## Billing / reconfigure

| Method | Path | Description | WHMCS |
|---|---|---|---|
| POST | `/v1/vms/{id}/suspend` | Stop + mark suspended (records prior power state). | `SuspendAccount` |
| POST | `/v1/vms/{id}/unsuspend` | Restore prior power state. | `UnsuspendAccount` |
| POST | `/v1/vms/{id}/password` | Reset password (`{"password": "..."}`). Applies on next boot. | — |
| POST | `/v1/vms/{id}/hostname` | Change hostname (`{"hostname": "..."}`). Applies on next boot. | — |
| POST | `/v1/vms/{id}/rebuild` | Reinstall from an image (`{"image_id"?, "password"?, "ssh_keys"?}`). Recreates the disk; data is lost. | — |
| POST | `/v1/vms/{id}/resize` | Change package (`{"flavor_id": "..."}`). Disk is grow-only; causes a brief reboot. | `ChangePackage` |

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
