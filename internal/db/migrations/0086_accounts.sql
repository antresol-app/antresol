-- External accounts per profile (Shikimori first) and their two-way sync.
--
-- account_links: one row per (profile, provider). Tokens are stored
-- encrypted (AES-256-GCM, see internal/accounts); never plaintext.
-- write_state is the profile's answer to "may we write back to the remote
-- list" (unasked | asked | enabled | declined). Times are unix seconds,
-- 0 = never.
CREATE TABLE account_links (
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider     TEXT NOT NULL,
  external_id  INTEGER NOT NULL DEFAULT 0,
  nickname     TEXT NOT NULL DEFAULT '',
  access_enc   BLOB,
  refresh_enc  BLOB,
  expires_at   INTEGER NOT NULL DEFAULT 0,
  scope        TEXT NOT NULL DEFAULT '',
  write_state  TEXT NOT NULL DEFAULT 'unasked' CHECK (write_state IN ('unasked','asked','enabled','declined')),
  status       TEXT NOT NULL DEFAULT 'ok',
  last_sync_at INTEGER NOT NULL DEFAULT 0,
  last_error   TEXT NOT NULL DEFAULT '',
  linked_at    INTEGER NOT NULL DEFAULT 0,
  -- 1 once a pull has imported the remote list (the first-link rule ran)
  first_pull_done INTEGER NOT NULL DEFAULT 0,
  -- remote entries the last pull could not map to a TMDB title
  unmapped     INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, provider)
);

-- account_rates: the remote list entries of a profile, one row per remote
-- title (target_id, the provider's own id; one TMDB title may have several,
-- e.g. the cours of an anime mapped to seasons of one TMDB series:
-- tmdb_season/tmdb_offset). rate_id is the provider's list entry id,
-- remote_status its own status (rewatching kept apart from watching);
-- remote_updated_at / pushed_at drive pull conflict resolution, mark_at
-- tells a push whether the mark changed since.
CREATE TABLE account_rates (
  user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider          TEXT NOT NULL,
  target_id         INTEGER NOT NULL,
  title_id          INTEGER NOT NULL REFERENCES tracked_titles(id) ON DELETE CASCADE,
  tmdb_season       INTEGER NOT NULL DEFAULT 0,
  tmdb_offset       INTEGER NOT NULL DEFAULT 0,
  rate_id           INTEGER NOT NULL DEFAULT 0,
  -- the remote title's episode count (0 = unknown, e.g. still airing)
  anime_episodes    INTEGER NOT NULL DEFAULT 0,
  -- the remote title's airing status (anons | ongoing | released; '' = unknown)
  anime_status      TEXT NOT NULL DEFAULT '',
  remote_status     TEXT NOT NULL DEFAULT '',
  -- the entry's episode count as of the last pull or our last write of it
  remote_episodes   INTEGER NOT NULL DEFAULT 0,
  -- Antresol's watched count in the cour's window when we last wrote an
  -- episode count to it (a lowered count goes out only while the remote
  -- still holds ours)
  local_episodes    INTEGER NOT NULL DEFAULT 0,
  remote_updated_at INTEGER NOT NULL DEFAULT 0,
  pushed_at         INTEGER NOT NULL DEFAULT 0,
  -- the title's mark time (our clock, or the remote change it was pulled
  -- from) as of its last pull win or push: set on every cour of the title
  -- alike; a mark whose time differs from it changed here since
  mark_at           INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, provider, target_id)
);

CREATE INDEX account_rates_title ON account_rates(title_id);

-- account_outbox: changes still to push. One row per title: the latest
-- intent wins (upserts coalesce, a later delete replaces them).
CREATE TABLE account_outbox (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider   TEXT NOT NULL,
  title_id   INTEGER NOT NULL REFERENCES tracked_titles(id) ON DELETE CASCADE,
  op         TEXT NOT NULL CHECK (op IN ('upsert','delete')),
  created_at INTEGER NOT NULL,
  attempts   INTEGER NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  -- after a failed attempt: not retried before this (unix seconds)
  next_at    INTEGER NOT NULL DEFAULT 0,
  -- bumped by every enqueue: a push removes the row only if no newer
  -- intent arrived while it ran
  seq        INTEGER NOT NULL DEFAULT 0,
  UNIQUE (user_id, provider, title_id)
);

CREATE INDEX account_outbox_title ON account_outbox(title_id);
CREATE INDEX tracked_titles_shikimori ON tracked_titles(shikimori_id) WHERE shikimori_id IS NOT NULL;

-- account_settings: per-provider OAuth client id and (encrypted) secret.
CREATE TABLE account_settings (
  provider   TEXT PRIMARY KEY,
  client_id  TEXT NOT NULL DEFAULT '',
  secret_enc BLOB,
  updated_at INTEGER NOT NULL DEFAULT 0
);

-- anime_map: MyAnimeList id -> TMDB title/season (with episode offset) and
-- AniDB id, loaded from a public mapping list. Shikimori ids equal MAL ids.
CREATE TABLE anime_map (
  mal_id      INTEGER PRIMARY KEY,
  kind        TEXT NOT NULL DEFAULT '',
  tmdb_id     INTEGER NOT NULL DEFAULT 0,
  tmdb_season INTEGER NOT NULL DEFAULT 0,
  tmdb_offset INTEGER NOT NULL DEFAULT 0,
  anidb_id    INTEGER NOT NULL DEFAULT 0,
  -- 'dataset' rows come from the mapping list; 'search' rows are cached TMDB
  -- search hits (cached_at, unix seconds, lets them expire).
  source      TEXT NOT NULL DEFAULT 'dataset',
  cached_at   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX anime_map_tmdb ON anime_map(tmdb_id);
