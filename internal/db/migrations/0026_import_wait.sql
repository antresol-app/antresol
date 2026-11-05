-- An import album waiting for a download it did not start (another job's,
-- or «На сервер»; state waiting_download, infohash = that download):
-- wait_title names it («Ждёт загрузку «…»»), wait_done/wait_at are its
-- bytes and when they last grew (no progress for 6 h: given up).
-- skip_infohash: a download given up on (paused, failed, stalled); the
-- album's own search leaves it out.
ALTER TABLE import_groups ADD COLUMN wait_title TEXT NOT NULL DEFAULT '';
ALTER TABLE import_groups ADD COLUMN wait_done INTEGER NOT NULL DEFAULT 0;
ALTER TABLE import_groups ADD COLUMN wait_at TEXT NULL;
ALTER TABLE import_groups ADD COLUMN skip_infohash TEXT NOT NULL DEFAULT '';
