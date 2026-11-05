-- Rotten Tomatoes scores of movies and series by IMDb id, read from
-- Wikidata (ratings.Service). A cache: rows may be dropped at any time.
-- 0 = no score; a row of zeros is a title Wikidata has none for.
CREATE TABLE IF NOT EXISTS rt_scores (
  imdb_id     TEXT PRIMARY KEY, -- tt0111161
  tomatometer INTEGER NOT NULL, -- percent
  audience    INTEGER NOT NULL, -- percent (the Popcornmeter)
  fetched_at  INTEGER NOT NULL  -- unix seconds
) WITHOUT ROWID;
