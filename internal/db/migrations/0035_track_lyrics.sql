-- Song words fetched online (LRCLIB) and kept, so a track's lyrics survive
-- the service being down. tracks.lyrics stays the words found in the
-- file's tags / .lrc (rewritten by every scan, and preferred when synced).
-- status: found (synced and/or plain), none (the service has none: asked
-- again after next_try_at), error (retried with backoff; attempts counts
-- the failures in a row). Tracks whose file has synced words are never
-- asked, so they have no row.
CREATE TABLE IF NOT EXISTS track_lyrics (
  item_id     INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  status      TEXT NOT NULL,
  synced      TEXT NOT NULL DEFAULT '',
  plain       TEXT NOT NULL DEFAULT '',
  source      TEXT NOT NULL DEFAULT 'lrclib',
  fetched_at  TEXT NOT NULL,    -- RFC 3339, the last attempt
  next_try_at TEXT NOT NULL,    -- RFC 3339
  attempts    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS track_lyrics_next ON track_lyrics(next_try_at);
