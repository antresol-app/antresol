-- files.probe_rev: a counter that moves whenever a file's probe_json is
-- set, rewritten or cleared (by the trigger, whoever writes it). The
-- fingerprint of a show's files (library.Repo.ShowFilesKey) sums it
-- instead of reading probe_json: that TEXT sits in overflow pages, and
-- every series card of a grid would otherwise read every episode's probe.
-- The covering index answers the fingerprint from the index alone (the
-- table row, with probe_json before missing_since, is never touched).
ALTER TABLE files ADD COLUMN probe_rev INTEGER NOT NULL DEFAULT 0;
UPDATE files SET probe_rev=1 WHERE probe_json IS NOT NULL;
CREATE INDEX IF NOT EXISTS files_item_probe_rev ON files(item_id, missing_since, probe_rev);
CREATE TRIGGER IF NOT EXISTS files_probe_rev AFTER UPDATE OF probe_json ON files
  WHEN old.probe_json IS NOT new.probe_json
BEGIN
  UPDATE files SET probe_rev = probe_rev + 1 WHERE id = new.id;
END;
