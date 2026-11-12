-- «ТВ» round 3: channels are one numbered list with favourites, and a
-- profile's last channel (a TV's «last channel», never media progress).

-- channel_favorites: the channels a profile starred.
CREATE TABLE channel_favorites (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  added_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY (user_id, channel_id)
);
CREATE INDEX channel_favorites_channel ON channel_favorites(channel_id);

-- channel_last: the channel a profile tuned in last.
CREATE TABLE channel_last (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- One clean list 1…N in the current order: shared channels first, then
-- personal ones (through negative numbers: number is UNIQUE).
UPDATE channels SET number = -number;
UPDATE channels SET number = (
  SELECT r.n FROM (
    SELECT id, ROW_NUMBER() OVER (ORDER BY (user_id IS NOT NULL), -number) AS n FROM channels
  ) r WHERE r.id = channels.id);

-- Schedules are rebuilt from now on: a title no longer airs on two
-- channels at once (the builder's new rule).
DELETE FROM channel_playout WHERE start_ms > CAST(strftime('%s','now') AS INTEGER) * 1000;
