-- «ТВ»: generated channels an owner deleted (internal/channels). A
-- generated channel is made again by the generator whenever the library has
-- enough for it, so deleting one has to be remembered by what the generator
-- keys it by (auto_key: "movies", "genre:комедия", "franchise:c10",
-- "mine:<profile>"…): the channel row itself, its schedule, numbers,
-- favourites and last-channel marks are deleted, this row stays and the
-- generator skips the key. name, category, edited: what the «Удалённые
-- каналы» list shows (the stored Russian name; edited: typed by hand, shown
-- as typed). user_id: the profile whose personal channel it was (NULL:
-- shared). Giving the channel back deletes the row; the next generator pass
-- makes it from the library as it is then.
CREATE TABLE channel_removed (
  id         INTEGER PRIMARY KEY,
  auto_key   TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  category   TEXT NOT NULL DEFAULT '',
  edited     INTEGER NOT NULL DEFAULT 0,
  user_id    INTEGER NULL REFERENCES users(id) ON DELETE CASCADE,
  removed_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
