-- TMDB text of library titles in a language other than the metadata
-- language (tmdb.Translations): title, overview, cast and episodes, as
-- JSON, fetched lazily when a viewer reads the library in that language.
-- A cache: rows may be dropped at any time.
CREATE TABLE IF NOT EXISTS tmdb_translations (
  kind       TEXT NOT NULL,    -- movie | tv
  tmdb_id    INTEGER NOT NULL,
  lang       TEXT NOT NULL,    -- TMDB tag: en-US, ru-RU
  data       TEXT NOT NULL,
  fetched_at INTEGER NOT NULL, -- unix seconds
  PRIMARY KEY (kind, tmdb_id, lang)
) WITHOUT ROWID;

-- A database that ran an earlier branch's 0032 (tmdb_translations under
-- that number) skips 0032_watch_time above: create it here too, so saving
-- progress still finds its table. A no-op everywhere else.
CREATE TABLE IF NOT EXISTS watch_time (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  day     TEXT NOT NULL,
  ms      INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, item_id, day)
);
CREATE INDEX IF NOT EXISTS watch_time_day ON watch_time(day);
