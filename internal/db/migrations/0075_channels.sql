-- «ТВ-каналы»: live channels made of the library (internal/channels).
-- user_id NULL: a shared channel every profile sees (admins edit it);
-- else the personal channel of that profile. rules_json: what it plays
-- (channels.Rules). auto_key: set on generated channels; edited: an admin
-- changed a generated channel (the generator leaves it alone then).
CREATE TABLE channels (
  id         INTEGER PRIMARY KEY,
  number     INTEGER NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  category   TEXT NOT NULL DEFAULT '',
  user_id    INTEGER NULL REFERENCES users(id) ON DELETE CASCADE,
  rules_json TEXT NOT NULL DEFAULT '{}',
  auto_key   TEXT NULL UNIQUE,
  enabled    INTEGER NOT NULL DEFAULT 1,
  edited     INTEGER NOT NULL DEFAULT 0,
  seed       INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX channels_user ON channels(user_id);

-- The materialised schedule: entry seq of a channel plays file_id (of
-- item_id, a movie or an episode; unit_id is the movie or the series)
-- from start_ms to end_ms (Unix ms). No foreign keys to the library: a
-- title that goes away leaves its rows, the builder cuts them.
CREATE TABLE channel_playout (
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  seq        INTEGER NOT NULL,
  start_ms   INTEGER NOT NULL,
  end_ms     INTEGER NOT NULL,
  item_id    INTEGER NOT NULL,
  file_id    INTEGER NOT NULL,
  unit_id    INTEGER NOT NULL,
  PRIMARY KEY (channel_id, seq)
);
CREATE INDEX channel_playout_time ON channel_playout(channel_id, end_ms);

-- The last item a channel played of each unit (a series' last episode;
-- unit 0: the ordered channel's last item).
CREATE TABLE channel_cursor (
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  unit_id    INTEGER NOT NULL,
  item_id    INTEGER NOT NULL,
  PRIMARY KEY (channel_id, unit_id)
);
