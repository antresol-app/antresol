-- watch_time: the time a user actually played of a movie, episode or
-- audiobook, per UTC day (YYYY-MM-DD), added up from the players'
-- progress reports (progress.PlayedAt): what «Статистика» sums. A progress
-- row holds only the last position, so the time played before this
-- migration cannot be recovered: the table starts empty.
CREATE TABLE IF NOT EXISTS watch_time (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  day     TEXT NOT NULL,
  ms      INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, item_id, day)
);
CREATE INDEX IF NOT EXISTS watch_time_day ON watch_time(day);
