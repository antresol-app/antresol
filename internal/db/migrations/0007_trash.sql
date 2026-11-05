-- File browser trash: items moved to MediaRoot/.vault-trash by the admin.
-- root + orig_path is the client address (root id, path relative to the root)
-- so a restore goes through the sandbox again; trash_path is relative to the
-- trash directory.
CREATE TABLE trash (
  id         INTEGER PRIMARY KEY,
  root       TEXT NOT NULL,
  orig_path  TEXT NOT NULL,
  trash_path TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  is_dir     INTEGER NOT NULL DEFAULT 0,
  size       INTEGER NOT NULL DEFAULT 0,
  deleted_by INTEGER NULL,
  deleted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX trash_deleted_at ON trash(deleted_at);
