-- Per-VM traffic accounting: combined in+out byte quota and usage for the
-- current period, plus the NIC link-block state used to cut network on overage.
ALTER TABLE vms ADD COLUMN traffic_quota_bytes  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vms ADD COLUMN traffic_used_bytes   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vms ADD COLUMN traffic_period_start TEXT    NOT NULL DEFAULT '';
ALTER TABLE vms ADD COLUMN network_blocked      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE vms ADD COLUMN network_block_reason TEXT    NOT NULL DEFAULT '';
