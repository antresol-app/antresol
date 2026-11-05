-- «Очищено»: voice-overs with the original speech subtracted (stage C of
-- the voice-over design), made in the
-- background by internal/voclean into DataDir/audio/<file_id>-<vo_index>.mka.
--
-- One row per voice-over track (vo_index: its ffprobe stream index) of a
-- library file, measured against orig_index (the original's track). state:
-- queued (found, not yet measured), done (path is the cleaned track),
-- not_eligible (measure_json says why; never retried while the file stays
-- the same), failed (fails counted, retried at retry_at; NULL retry_at:
-- given up). file_size/file_mtime: the file as it was measured — a file
-- replaced by a new download no longer matches, and its row and track are
-- removed and measured again. start_ms: where the cleaned track's first
-- sample sits on the source file's timestamps (the file's start_time):
-- the cleaned track is on the file's timeline from its start, a voice-over
-- stream that starts late padded with silence.
CREATE TABLE cleaned_audio (
  file_id      INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  vo_index     INTEGER NOT NULL,
  orig_index   INTEGER NOT NULL,
  studio       TEXT NOT NULL DEFAULT '',
  lang         TEXT NOT NULL DEFAULT '',
  title        TEXT NOT NULL DEFAULT '',
  state        TEXT NOT NULL DEFAULT 'queued',
  method       TEXT NOT NULL DEFAULT 'subtraction',
  path         TEXT NOT NULL DEFAULT '',
  measure_json TEXT NOT NULL DEFAULT '',
  file_size    INTEGER NOT NULL DEFAULT 0,
  file_mtime   INTEGER NOT NULL DEFAULT 0,
  start_ms     REAL NOT NULL DEFAULT 0,
  fails        INTEGER NOT NULL DEFAULT 0,
  retry_at     TEXT NULL,
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY (file_id, vo_index)
);
