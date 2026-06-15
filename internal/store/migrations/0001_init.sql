CREATE TABLE vms (
    id               TEXT PRIMARY KEY,
    domain_name      TEXT NOT NULL,
    domain_uuid      TEXT NOT NULL DEFAULT '',
    hostname         TEXT NOT NULL,
    flavor_id        TEXT NOT NULL,
    image_id         TEXT NOT NULL,
    vcpus            INTEGER NOT NULL,
    memory_mb        INTEGER NOT NULL,
    disk_gb          INTEGER NOT NULL,
    network_id       TEXT NOT NULL,
    network_mode     TEXT NOT NULL,
    mac              TEXT NOT NULL DEFAULT '',
    ip               TEXT NOT NULL DEFAULT '',
    gateway          TEXT NOT NULL DEFAULT '',
    netmask          TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL,
    power_state      TEXT NOT NULL,
    prev_power_state TEXT NOT NULL DEFAULT '',
    disk_path        TEXT NOT NULL DEFAULT '',
    seed_path        TEXT NOT NULL DEFAULT '',
    ssh_keys         TEXT NOT NULL DEFAULT '[]',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE TABLE jobs (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,
    vm_id           TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL,
    error           TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    started_at      TEXT,
    finished_at     TEXT
);

CREATE INDEX idx_jobs_state ON jobs (state);
CREATE INDEX idx_jobs_vm ON jobs (vm_id);

CREATE TABLE idempotency_keys (
    key          TEXT PRIMARY KEY,
    job_id       TEXT NOT NULL DEFAULT '',
    request_hash TEXT NOT NULL,
    response     BLOB,
    status_code  INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL
);

CREATE TABLE ip_allocations (
    network_id TEXT NOT NULL,
    ip         TEXT NOT NULL,
    vm_id      TEXT NOT NULL,
    mac        TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (network_id, ip)
);

CREATE INDEX idx_ipalloc_vm ON ip_allocations (vm_id);
