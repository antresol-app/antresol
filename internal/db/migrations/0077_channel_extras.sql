-- «ТВ» round 3 (G): a channel may also play titles that are not in the
-- library (channels.Rules.Extras, found and played by channels.Extras).
-- channel_extras keeps each channel's candidates (asked again daily), so
-- the schedule stays materialised: a schedule row with extra_id set plays
-- that candidate (its item_id is then -extra_id, its file_id 0).
-- current: in the last candidate list; failed_until_ms: it would not
-- play, left out of new schedules until then. Rows a schedule still names
-- are kept when the list changes.
CREATE TABLE channel_extras (
  id              INTEGER PRIMARY KEY,
  channel_id      INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  key             TEXT NOT NULL,
  kind            TEXT NOT NULL DEFAULT 'movie',
  tmdb_id         INTEGER NOT NULL DEFAULT 0,
  title           TEXT NOT NULL,
  year            INTEGER NOT NULL DEFAULT 0,
  overview        TEXT NOT NULL DEFAULT '',
  rating          TEXT NOT NULL DEFAULT '',
  genres_json     TEXT NOT NULL DEFAULT '[]',
  poster          TEXT NOT NULL DEFAULT '',
  backdrop        TEXT NOT NULL DEFAULT '',
  runtime_ms      INTEGER NOT NULL,
  season          INTEGER NOT NULL DEFAULT 0,
  episode         INTEGER NOT NULL DEFAULT 0,
  current         INTEGER NOT NULL DEFAULT 1,
  failed_until_ms INTEGER NOT NULL DEFAULT 0,
  fetched_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE (channel_id, key)
);

-- when the channel's candidates were last asked for (Unix ms; 0: never)
ALTER TABLE channels ADD COLUMN extras_at_ms INTEGER NOT NULL DEFAULT 0;

-- a schedule row of a candidate (NULL: a library file)
ALTER TABLE channel_playout ADD COLUMN extra_id INTEGER NULL;
