-- Music library import from streaming services (plan 6). Timestamps are
-- RFC3339 UTC text.

-- One import run: a snapshot of a service's library, previewed, then
-- matched to MusicBrainz and downloaded. status: reading, preview,
-- matching, downloading, done, failed, cancelled.
CREATE TABLE IF NOT EXISTS import_jobs (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  service     TEXT NOT NULL,
  account     TEXT NOT NULL DEFAULT '',
  status      TEXT NOT NULL,
  favorites   INTEGER NOT NULL DEFAULT 1, -- liked songs become «Избранное»
  error       TEXT NOT NULL DEFAULT '',
  report_json TEXT NOT NULL DEFAULT '{}',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  started_at  TEXT NULL,
  finished_at TEXT NULL
);
CREATE INDEX IF NOT EXISTS import_jobs_user ON import_jobs(user_id, id);
CREATE INDEX IF NOT EXISTS import_jobs_status ON import_jobs(status);

-- One album to acquire (a MusicBrainz release group) with the job's tracks
-- on it. state: waiting, searching, waiting_space, downloading, done, have,
-- not_found, failed.
CREATE TABLE IF NOT EXISTS import_groups (
  id            INTEGER PRIMARY KEY,
  job_id        INTEGER NOT NULL REFERENCES import_jobs(id) ON DELETE CASCADE,
  release_group TEXT NOT NULL,
  artist        TEXT NOT NULL,
  album         TEXT NOT NULL,
  year          INTEGER NOT NULL DEFAULT 0,
  aliases_json  TEXT NOT NULL DEFAULT '[]',
  album_item_id INTEGER NULL REFERENCES items(id) ON DELETE SET NULL,
  state         TEXT NOT NULL DEFAULT 'waiting',
  infohash      TEXT NOT NULL DEFAULT '',
  bytes         INTEGER NOT NULL DEFAULT 0,
  attempts      INTEGER NOT NULL DEFAULT 0,
  error         TEXT NOT NULL DEFAULT '',
  updated_at    TEXT NOT NULL,
  UNIQUE(job_id, release_group)
);

-- One song of the snapshot. state: pending, matched, review, skipped, have,
-- queued, downloading, done, not_found, failed.
CREATE TABLE IF NOT EXISTS import_tracks (
  id               INTEGER PRIMARY KEY,
  job_id           INTEGER NOT NULL REFERENCES import_jobs(id) ON DELETE CASCADE,
  key              TEXT NOT NULL,
  artist           TEXT NOT NULL DEFAULT '',
  title            TEXT NOT NULL DEFAULT '',
  album            TEXT NOT NULL DEFAULT '',
  album_artist     TEXT NOT NULL DEFAULT '',
  isrc             TEXT NOT NULL DEFAULT '',
  duration_ms      INTEGER NOT NULL DEFAULT 0,
  added_at         TEXT NULL,
  liked            INTEGER NOT NULL DEFAULT 0,
  state            TEXT NOT NULL DEFAULT 'pending',
  reason           TEXT NOT NULL DEFAULT '',
  mb_recording     TEXT NOT NULL DEFAULT '',
  mb_release_group TEXT NOT NULL DEFAULT '',
  mb_title         TEXT NOT NULL DEFAULT '',
  mb_artist        TEXT NOT NULL DEFAULT '',
  mb_album         TEXT NOT NULL DEFAULT '',
  mb_year          INTEGER NOT NULL DEFAULT 0,
  group_id         INTEGER NULL REFERENCES import_groups(id) ON DELETE SET NULL,
  item_id          INTEGER NULL REFERENCES items(id) ON DELETE SET NULL,
  UNIQUE(job_id, key)
);
CREATE INDEX IF NOT EXISTS import_tracks_state ON import_tracks(job_id, state);
CREATE INDEX IF NOT EXISTS import_tracks_group ON import_tracks(group_id);

-- A service playlist, kept across jobs (a re-run updates the same Vault
-- playlist): job_id is the latest job that read it.
CREATE TABLE IF NOT EXISTS import_playlists (
  id          INTEGER PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  service     TEXT NOT NULL,
  external_id TEXT NOT NULL,
  name        TEXT NOT NULL,
  readable    INTEGER NOT NULL DEFAULT 1,
  playlist_id INTEGER NULL REFERENCES playlists(id) ON DELETE SET NULL,
  job_id      INTEGER NULL REFERENCES import_jobs(id) ON DELETE SET NULL,
  selected    INTEGER NOT NULL DEFAULT 1,
  UNIQUE(user_id, service, external_id)
);
CREATE INDEX IF NOT EXISTS import_playlists_job ON import_playlists(job_id);

-- The playlist's songs in the service's order (of its latest job).
CREATE TABLE IF NOT EXISTS import_playlist_entries (
  import_playlist_id INTEGER NOT NULL REFERENCES import_playlists(id) ON DELETE CASCADE,
  position           INTEGER NOT NULL,
  track_id           INTEGER NOT NULL REFERENCES import_tracks(id) ON DELETE CASCADE,
  PRIMARY KEY(import_playlist_id, position)
);
CREATE INDEX IF NOT EXISTS import_playlist_entries_track ON import_playlist_entries(track_id);

-- Saved albums of the snapshot (reported, not downloaded).
CREATE TABLE IF NOT EXISTS import_albums (
  job_id   INTEGER NOT NULL REFERENCES import_jobs(id) ON DELETE CASCADE,
  key      TEXT NOT NULL,
  artist   TEXT NOT NULL DEFAULT '',
  title    TEXT NOT NULL DEFAULT '',
  year     INTEGER NOT NULL DEFAULT 0,
  added_at TEXT NULL,
  item_id  INTEGER NULL REFERENCES items(id) ON DELETE SET NULL,
  PRIMARY KEY(job_id, key)
);
