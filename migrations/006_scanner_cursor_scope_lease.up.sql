-- Per-scope scan leases so multiple replicas do not forward-scan the same scope.

ALTER TABLE scanner_cursors
  ADD COLUMN IF NOT EXISTS locked_by text,
  ADD COLUMN IF NOT EXISTS locked_until timestamptz;

CREATE INDEX IF NOT EXISTS scanner_cursors_locked_until_idx
  ON scanner_cursors (locked_until)
  WHERE locked_until IS NOT NULL;
