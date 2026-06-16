-- Track how many times a job has been attempted, for bounded retry with backoff.
ALTER TABLE jobs ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
