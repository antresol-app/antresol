-- «Уведомления на почту» (internal/notify).
--
-- notify_prefs: a profile's address and what it wants to hear about (an
-- adult's own choice, a kid's the admin's). Every topic is off until the
-- profile turns it on; nothing is sent to an address that its own
-- verification letter did not confirm (verified = the confirmed address,
-- so a changed address is unconfirmed again). <topic>_at: Unix seconds
-- when the topic was last turned on (what happened before is no news).
-- lang: the language of the letters (ru | en; '' the server's).
-- last_sent_at: the last digest (at most one an hour). verify_*: the last
-- verification letter (when, the hash of its code with the address,
-- tries). bounced: an address the mail server refused for good («Адрес не
-- принимает письма»; unconfirmed until confirmed again). fail_count,
-- next_try_at: a digest that failed for a while waits longer each time.
CREATE TABLE notify_prefs (
  user_id        INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  email          TEXT NOT NULL DEFAULT '',
  verified       TEXT NOT NULL DEFAULT '',
  episodes       INTEGER NOT NULL DEFAULT 0,
  available      INTEGER NOT NULL DEFAULT 0,
  requests       INTEGER NOT NULL DEFAULT 0,
  downloads      INTEGER NOT NULL DEFAULT 0,
  episodes_at    INTEGER NOT NULL DEFAULT 0,
  available_at   INTEGER NOT NULL DEFAULT 0,
  requests_at    INTEGER NOT NULL DEFAULT 0,
  downloads_at   INTEGER NOT NULL DEFAULT 0,
  quiet_hours    INTEGER NOT NULL DEFAULT 1,
  lang           TEXT NOT NULL DEFAULT '',
  last_sent_at   INTEGER NOT NULL DEFAULT 0,
  verify_sent_at INTEGER NOT NULL DEFAULT 0,
  verify_code    TEXT NOT NULL DEFAULT '',
  verify_tries   INTEGER NOT NULL DEFAULT 0,
  bounced        TEXT NOT NULL DEFAULT '',
  fail_count     INTEGER NOT NULL DEFAULT 0,
  next_try_at    INTEGER NOT NULL DEFAULT 0,
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- notify_verify_log: the verification letters of the last day (a cap per
-- profile and per address; addr: a hash of the address).
CREATE TABLE notify_verify_log (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  addr    TEXT NOT NULL,
  at      INTEGER NOT NULL
);
CREATE INDEX notify_verify_log_at ON notify_verify_log(at);

-- notify_events: what waits for a profile's next digest, and what went
-- (sent_at, kept a while so the same thing is never queued twice: key is
-- unique per profile, e.g. "ep:<series>:S1E2", "dl:<infohash>:done").
-- topic: episodes | available | requests | downloads. item_id: the
-- library item the letter links to; group_id: the series several episodes
-- go under. data_json: what the letter says (titles, numbers, a TMDB
-- poster). sending_at: the letter with it was being sent (a crash during
-- the send: after 10 minutes it counts as sent, never sent twice).
CREATE TABLE notify_events (
  id         INTEGER PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  topic      TEXT NOT NULL,
  key        TEXT NOT NULL,
  item_id    INTEGER NOT NULL DEFAULT 0,
  group_id   INTEGER NOT NULL DEFAULT 0,
  data_json  TEXT NOT NULL DEFAULT '{}',
  created_at INTEGER NOT NULL,
  sending_at INTEGER NULL,
  sent_at    INTEGER NULL,
  UNIQUE (user_id, key)
);
CREATE INDEX notify_events_pending ON notify_events(user_id) WHERE sent_at IS NULL;

-- notify_seen: what a profile was told about, beyond the events' keys —
-- an episode's file path, a watchlist title back on disk — so a row made
-- again (a restore, a rescan after a move) is no news. at: Unix seconds.
CREATE TABLE notify_seen (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key     TEXT NOT NULL,
  at      INTEGER NOT NULL,
  PRIMARY KEY (user_id, key)
);

-- notify_state: the collector's own marks (how far it looked at the
-- library, the watchlist titles that were missing from disk).
CREATE TABLE notify_state (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
