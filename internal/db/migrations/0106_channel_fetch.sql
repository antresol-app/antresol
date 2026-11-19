-- «ТВ»: channels whose titles are fetched before they air and removed
-- after (channels.Rules.Fetch, internal/channels/fetch*.go).
-- channel_fetch: one row per programme of such a channel (a movie, an
-- episode), in the channel's order (pos; -1: no longer in it, kept until
-- what was fetched for it is cleaned up). No foreign key to channels: a
-- deleted channel's fetched files still have to be cleaned up.
--   state: wanted (not on disk) | requested (an addon fetches it) |
--          ready (fetched for the channel, in the library) | library (on
--          disk, not fetched for the channel: never removed by it) | kept
--          (fetched for it, then someone in the household kept it) |
--          removed (cleaned up after it aired) | failed (until failed_until_ms)
--   existing: the addon said another download of it ran already (it
--          lands as library, never removed by the channel)
--   item_id, files_json: the library item and its files once it arrived
--   size_bytes, done_bytes: the download's size and progress the addon said
--   aired_ms: the end of its last airing on the channel (0: not aired)
CREATE TABLE channel_fetch (
  id              INTEGER PRIMARY KEY,
  channel_id      INTEGER NOT NULL,
  pos             INTEGER NOT NULL,
  kind            TEXT NOT NULL,
  tmdb_id         INTEGER NOT NULL,
  season          INTEGER NOT NULL DEFAULT 0,
  episode         INTEGER NOT NULL DEFAULT 0,
  title           TEXT NOT NULL,
  episode_title   TEXT NOT NULL DEFAULT '',
  year            INTEGER NOT NULL DEFAULT 0,
  overview        TEXT NOT NULL DEFAULT '',
  rating          TEXT NOT NULL DEFAULT '',
  poster          TEXT NOT NULL DEFAULT '',
  runtime_ms      INTEGER NOT NULL,
  state           TEXT NOT NULL DEFAULT 'wanted',
  existing        INTEGER NOT NULL DEFAULT 0,
  item_id         INTEGER NOT NULL DEFAULT 0,
  files_json      TEXT NOT NULL DEFAULT '[]',
  size_bytes      INTEGER NOT NULL DEFAULT 0,
  done_bytes      INTEGER NOT NULL DEFAULT 0,
  requested_ms    INTEGER NOT NULL DEFAULT 0,
  ready_ms        INTEGER NOT NULL DEFAULT 0,
  aired_ms        INTEGER NOT NULL DEFAULT 0,
  failed_until_ms INTEGER NOT NULL DEFAULT 0,
  error           TEXT NOT NULL DEFAULT '',
  UNIQUE (channel_id, kind, tmdb_id, season, episode)
);
CREATE INDEX channel_fetch_item ON channel_fetch(item_id);

-- a schedule row of a programme not on disk yet (its item_id is then
-- channels.fetchItem(fetch_id), its file_id 0); NULL: a library file or a
-- title outside the library
ALTER TABLE channel_playout ADD COLUMN fetch_id INTEGER NULL;

-- the rules' titles the channel_fetch rows were made from (a hash; '' never)
ALTER TABLE channels ADD COLUMN fetch_hash TEXT NOT NULL DEFAULT '';
