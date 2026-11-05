-- The fit of an external audio track to its video (stage B of the
-- voice-over design, internal/vofit):
-- the offset (ms, positive: later on the video's timeline) and tempo
-- (atempo; 1: none) the players apply.
--
-- One row per track «Найти озвучку…» placed (path) beside the library file
-- file_id (a release's own tracks, stage A, have none and play as before);
-- orig_lang: the title's original language (ISO 639-1, '' unknown), the
-- track it is measured against. state:
-- pending (not fitted yet), ok (fitted: the only state offered), misfit
-- (another cut), unmeasured (no original-language track, a silent or too
-- short original), failed (fails counted, retried at retry_at; NULL:
-- given up). track_size/track_mtime and file_size/file_mtime: the track
-- and the video as fitted — either changed, the track is hidden and
-- fitted again. Deleting the file deletes its rows.
CREATE TABLE external_audio_fit (
  path         TEXT PRIMARY KEY,
  file_id      INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  state        TEXT NOT NULL DEFAULT 'pending',
  orig_lang    TEXT NOT NULL DEFAULT '',
  offset_ms    REAL NOT NULL DEFAULT 0,
  tempo        REAL NOT NULL DEFAULT 1,
  measure_json TEXT NOT NULL DEFAULT '',
  track_size   INTEGER NOT NULL DEFAULT 0,
  track_mtime  INTEGER NOT NULL DEFAULT 0,
  file_size    INTEGER NOT NULL DEFAULT 0,
  file_mtime   INTEGER NOT NULL DEFAULT 0,
  fails        INTEGER NOT NULL DEFAULT 0,
  retry_at     TEXT NULL,
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX external_audio_fit_file ON external_audio_fit(file_id);
