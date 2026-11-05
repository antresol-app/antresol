-- TMDB's titles around a library title (tmdb.Related): its
-- recommendations and similar titles with the collection it belongs to
-- (scope movie | tv), and the collections' parts (scope collection), as
-- JSON in one language, for the «Похожие» and «Коллекция» rows of title
-- pages. A cache: rows may be dropped at any time.
CREATE TABLE IF NOT EXISTS tmdb_related (
  scope      TEXT NOT NULL,    -- movie | tv | collection
  tmdb_id    INTEGER NOT NULL,
  lang       TEXT NOT NULL,    -- TMDB tag: en-US, ru-RU
  data       TEXT NOT NULL,
  fetched_at INTEGER NOT NULL, -- unix seconds
  PRIMARY KEY (scope, tmdb_id, lang)
) WITHOUT ROWID;
