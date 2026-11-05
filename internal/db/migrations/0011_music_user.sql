-- Plan 2: per-user music state (playlists, stars, play history).
CREATE TABLE playlists (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE playlist_items (
  playlist_id INTEGER NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
  idx         INTEGER NOT NULL,
  item_id     INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  PRIMARY KEY (playlist_id, idx)
);

CREATE TABLE stars (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  starred_at TEXT NOT NULL,
  PRIMARY KEY (user_id, item_id)
);

CREATE TABLE plays (
  user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id   INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  played_at TEXT NOT NULL
);
CREATE INDEX plays_user_time ON plays(user_id, played_at);
