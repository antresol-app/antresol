-- «Попросить»: a kid profile asks for a title that is not in the library;
-- an admin approves or declines.
-- One row per kid and title (title_key: "tmdb:<id>" for movies and series,
-- "ext:<catalog id>" for albums and books, "name:<folded title>" without
-- one). status is what the admin decided: pending | approved | declined;
-- «Уже в медиатеке» is not stored, the library is asked when listing.
-- The extra columns are what the release picker needs to search for it.
CREATE TABLE IF NOT EXISTS kid_requests (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL,              -- movie | series | music | book | audiobook | comic
  title_key    TEXT NOT NULL,
  name_key     TEXT NOT NULL DEFAULT '',  -- "name:<artist> — <title>" of an album or book (an admin's download without the catalog id matches it)
  tmdb_id      INTEGER NOT NULL DEFAULT 0,
  ext_id       TEXT NOT NULL DEFAULT '',
  title        TEXT NOT NULL,
  subtitle     TEXT NOT NULL DEFAULT '',   -- the artist or the author
  year         INTEGER NOT NULL DEFAULT 0,
  poster       TEXT NOT NULL DEFAULT '',
  aliases_json TEXT NOT NULL DEFAULT '[]',
  rating       TEXT NOT NULL DEFAULT '',   -- the age rating when it was asked for (video)
  status       TEXT NOT NULL DEFAULT 'pending',
  reason       TEXT NOT NULL DEFAULT '',   -- why it was declined (optional)
  created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  decided_at   TEXT NULL,
  decided_by   INTEGER NULL REFERENCES users(id) ON DELETE SET NULL,
  UNIQUE (user_id, kind, title_key)
);
CREATE INDEX IF NOT EXISTS kid_requests_title ON kid_requests(kind, title_key);
CREATE INDEX IF NOT EXISTS kid_requests_name ON kid_requests(kind, name_key);
CREATE INDEX IF NOT EXISTS kid_requests_status ON kid_requests(user_id, status);
