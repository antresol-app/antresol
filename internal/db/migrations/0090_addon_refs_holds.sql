-- Addon file references and holds (internal/addons, RefStore).
-- addon_file_refs: the library files an addon brought in, under the
-- addon's own reference string. One addon per file: a file another addon
-- references is never re-bound. The row goes with its file.
CREATE TABLE addon_file_refs (
  file_id  INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
  addon_id TEXT NOT NULL,
  ref      TEXT NOT NULL
);
CREATE INDEX addon_file_refs_ref ON addon_file_refs(addon_id, ref);
-- addon_holds: each addon's last pushed set of holds (JSON), kept so held
-- files stay protected while the addon is down or the server restarts.
CREATE TABLE addon_holds (addon_id TEXT PRIMARY KEY, holds_json TEXT NOT NULL, updated_at INTEGER NOT NULL);
