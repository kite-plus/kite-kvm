-- The initial cloud-init password is needed when the async create/password job
-- runs, so it is persisted here. It is never serialized in API responses.
ALTER TABLE vms ADD COLUMN password TEXT NOT NULL DEFAULT '';
