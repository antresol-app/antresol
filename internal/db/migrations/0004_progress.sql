CREATE TABLE progress (
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id     INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  position_ms INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  finished    INTEGER NOT NULL DEFAULT 0,
  updated_at  TEXT NOT NULL,
  PRIMARY KEY (user_id, item_id)
);
CREATE INDEX progress_user_updated ON progress(user_id, updated_at);

CREATE TABLE watchlist (
  user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id  INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  added_at TEXT NOT NULL,
  PRIMARY KEY (user_id, item_id)
);
