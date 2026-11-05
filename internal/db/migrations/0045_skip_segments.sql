-- Skip segments of the library's video files: recap, intro and credits
-- (positions in ms on the file timeline, the clock of the subtitles), kept
-- per source so a better source wins at play time (chapters > theintrodb >
-- introdb > analysis) and one source can be redone alone.
CREATE TABLE file_segments (
  file_id  INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  kind     TEXT NOT NULL,    -- recap | intro | credits
  source   TEXT NOT NULL,    -- chapters | theintrodb | introdb | analysis
  start_ms INTEGER NOT NULL,
  end_ms   INTEGER NOT NULL,
  PRIMARY KEY (file_id, kind, source)
);

-- What has been looked at for a file, so a pass redoes only what is due:
-- the file as it was (size, mtime: a replaced file starts over), the
-- version of the chapter reading and of the analysis (a better algorithm
-- runs again), and the online lookup's ids (they change once the metadata
-- is enriched) and time.
CREATE TABLE segment_status (
  file_id    INTEGER PRIMARY KEY REFERENCES files(id) ON DELETE CASCADE,
  size       INTEGER NOT NULL,
  mtime      INTEGER NOT NULL,
  chapters_v INTEGER NOT NULL DEFAULT 0,
  online_key TEXT NOT NULL DEFAULT '',
  online_at  INTEGER NOT NULL DEFAULT 0,
  analysis_v INTEGER NOT NULL DEFAULT 0
);

-- The online databases' answers per episode or movie (one request each,
-- «nothing there» included), re-asked after 30 days. segments is the JSON
-- of the answer's segments; group_ms the duration of the release version
-- they were measured on (0: unknown).
CREATE TABLE segment_lookups (
  service    TEXT NOT NULL,  -- theintrodb | introdb
  lookup     TEXT NOT NULL,  -- tmdb:1418/1/2, imdb:tt0898266/1/2, tmdb:603
  fetched_at INTEGER NOT NULL,
  segments   TEXT NOT NULL DEFAULT '[]',
  group_ms   INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (service, lookup)
);
