-- Ongoing tracking: which titles (series/movies, library or not) the
-- household follows, who marked what, and where each episode can be got.
--
-- tracked_titles: one row per TMDB title. Other ids (Shikimori, MyShows,
-- AniDB) are optional cross-references. status is the airing state
-- (airing | ended | upcoming | unknown); next_check is when the refresher
-- should look at the title again (NULL = never checked, due now). cert is
-- the age certification TMDB gives (RU form: 0+ 6+ 12+ 16+ 18+; '' =
-- unknown), the rating gate of titles that are not in the library.
CREATE TABLE tracked_titles (
  id           INTEGER PRIMARY KEY,
  kind         TEXT NOT NULL,                 -- series | movie
  tmdb_id      INTEGER NOT NULL,
  shikimori_id INTEGER,
  myshows_id   INTEGER,
  anidb_id     INTEGER,
  imdb_id      TEXT NOT NULL DEFAULT '',
  name         TEXT NOT NULL,
  poster       TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL DEFAULT 'unknown',
  cert         TEXT NOT NULL DEFAULT '',
  next_check   TEXT,
  refreshed_at TEXT,
  UNIQUE(kind, tmdb_id)
);
CREATE INDEX tracked_titles_next_check ON tracked_titles(next_check);

-- title_marks: per-profile list state of a title (planned, watching, ...).
-- origin tells where the mark came from (antresol | shikimori | myshows).
CREATE TABLE title_marks (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title_id   INTEGER NOT NULL REFERENCES tracked_titles(id) ON DELETE CASCADE,
  state      TEXT NOT NULL CHECK (state IN ('planned','watching','completed','on_hold','dropped')),
  score      INTEGER CHECK (score IS NULL OR (score BETWEEN 1 AND 10)),
  origin     TEXT NOT NULL DEFAULT 'antresol',
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, title_id)
);
CREATE INDEX title_marks_title ON title_marks(title_id);

-- title_episodes: the episode list of a title as last fetched (air_date is
-- YYYY-MM-DD or empty when unknown). Replaced wholesale on each refresh.
CREATE TABLE title_episodes (
  title_id INTEGER NOT NULL REFERENCES tracked_titles(id) ON DELETE CASCADE,
  season   INTEGER NOT NULL,
  number   INTEGER NOT NULL,
  name     TEXT NOT NULL DEFAULT '',
  air_date TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (title_id, season, number)
);

-- episode_sources: where an episode can be watched or fetched
-- (library | pinned | torrent | online). ref identifies the source item;
-- first_seen lets the feed show what appeared recently.
CREATE TABLE episode_sources (
  title_id    INTEGER NOT NULL REFERENCES tracked_titles(id) ON DELETE CASCADE,
  season      INTEGER NOT NULL,
  episode     INTEGER NOT NULL,
  kind        TEXT NOT NULL,
  ref         TEXT NOT NULL,
  provider    TEXT NOT NULL DEFAULT '',
  quality     TEXT NOT NULL DEFAULT '',
  labels_json TEXT NOT NULL DEFAULT '[]',
  first_seen  TEXT NOT NULL,
  last_seen   TEXT NOT NULL,
  PRIMARY KEY (title_id, season, episode, kind, ref)
);
CREATE INDEX episode_sources_first_seen ON episode_sources(first_seen);

-- tracked_scans: the source kinds that have scanned a title successfully at
-- least once. A kind's first successful scan is its baseline: what it finds
-- then is old news and is mailed to nobody.
CREATE TABLE tracked_scans (
  title_id INTEGER NOT NULL REFERENCES tracked_titles(id) ON DELETE CASCADE,
  kind     TEXT NOT NULL,
  PRIMARY KEY (title_id, kind)
);
