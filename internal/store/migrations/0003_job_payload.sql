-- Jobs can carry operation parameters (e.g. a snapshot name) to the async
-- runner. Stored as a small JSON object.
ALTER TABLE jobs ADD COLUMN payload TEXT NOT NULL DEFAULT '{}';
