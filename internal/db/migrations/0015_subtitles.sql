-- Plan 2 Task 12: automatic subtitles. One row per (file, language) that
-- OpenSubtitles was asked about: result found | none | error | quota.
-- found/none/error wait 7 days before the next try; quota waits for the
-- daily reset only.
CREATE TABLE subtitle_attempts (
  file_id  INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  lang     TEXT NOT NULL,
  tried_at TEXT NOT NULL,
  result   TEXT NOT NULL,
  PRIMARY KEY (file_id, lang)
);
