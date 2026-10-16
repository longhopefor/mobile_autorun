ALTER TABLE tasks ADD COLUMN archived_at timestamptz;
CREATE INDEX tasks_owner_archive_created_idx ON tasks(owner, (archived_at IS NOT NULL), created_at DESC, id DESC);
