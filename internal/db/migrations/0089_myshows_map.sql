-- MyShows show id -> TMDB title cache (read-only MyShows import).
--
-- MyShows ids are their own; a show is mapped through its IMDb id and a TMDB
-- find. tmdb_id 0 is a known miss (no IMDb id, or TMDB does not know it):
-- retried after 30 days, so a later TMDB addition is picked up. imdb_id is
-- 'tt' + 7 digits or '' when MyShows has none; kind is 'series' | 'movie' ('' on a
-- miss). cached_at is unix seconds.
CREATE TABLE myshows_map (
  show_id   INTEGER PRIMARY KEY,
  imdb_id   TEXT NOT NULL DEFAULT '',
  tmdb_id   INTEGER NOT NULL DEFAULT 0,
  kind      TEXT NOT NULL DEFAULT '',
  cached_at INTEGER NOT NULL DEFAULT 0
);

-- The last seen state of every entry of a profile's MyShows list, mapped or
-- not (MyShows gives no per-entry times: a pull applies an entry only when
-- it is new since the link or its status/score changed). status is the
-- MyShows watchStatus, score its rating 0..5 (0 = none), first_seen unix
-- seconds of the pull that first saw it. Cleared on unlink.
CREATE TABLE myshows_entries (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  show_id    INTEGER NOT NULL,
  status     TEXT NOT NULL DEFAULT '',
  score      INTEGER NOT NULL DEFAULT 0,
  first_seen INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, show_id)
);
