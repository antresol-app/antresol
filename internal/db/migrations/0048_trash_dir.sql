-- trash.trash_dir: which trash an item is in. A rename never leaves its
-- mount, so a path on another mount than MediaRoot goes to that mount's own
-- <mount point>/.vault-trash (stored here, absolute); '' is MediaRoot's
-- trash, as before. trash_path is relative to that trash, unique within it.
CREATE TABLE trash_new (
  id         INTEGER PRIMARY KEY,
  root       TEXT NOT NULL,
  orig_path  TEXT NOT NULL,
  trash_path TEXT NOT NULL,
  trash_dir  TEXT NOT NULL DEFAULT '',
  name       TEXT NOT NULL,
  is_dir     INTEGER NOT NULL DEFAULT 0,
  size       INTEGER NOT NULL DEFAULT 0,
  deleted_by INTEGER NULL,
  deleted_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE (trash_dir, trash_path)
);
INSERT INTO trash_new(id, root, orig_path, trash_path, name, is_dir, size, deleted_by, deleted_at)
  SELECT id, root, orig_path, trash_path, name, is_dir, size, deleted_by, deleted_at FROM trash;
DROP TABLE trash;
ALTER TABLE trash_new RENAME TO trash;
CREATE INDEX trash_deleted_at ON trash(deleted_at);
