-- «Ключи API и ИИ» (internal/auth/api_keys.go): a profile's personal API
-- keys for scripts and AI assistants (the /api routes and the MCP server at
-- /mcp). hash: the SHA-256 of the key (the key itself is shown once, on
-- creation); hint: its last four characters, for the list. scope: read
-- (GET only), act (reads and actions), admin (an admin's own rights).
-- last_used_at/last_used_from: when and from which address it was last
-- used (written at most once a minute).
CREATE TABLE api_keys (
  id             INTEGER PRIMARY KEY,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name           TEXT NOT NULL,
  scope          TEXT NOT NULL CHECK (scope IN ('read', 'act', 'admin')),
  hash           TEXT NOT NULL UNIQUE,
  hint           TEXT NOT NULL,
  created_at     TEXT NOT NULL,
  last_used_at   TEXT NULL,
  last_used_from TEXT NOT NULL DEFAULT ''
);
CREATE INDEX api_keys_user ON api_keys(user_id);

-- What the keys changed: every request of a key that is not a read, and
-- every refusal by the key's scope. key_name is kept so a revoked key's
-- rows still say whose they were (key_id is then NULL). via: the MCP tool
-- the request was made for ("" for a direct API call). Trimmed to the
-- latest rows of each profile.
CREATE TABLE api_key_log (
  id       INTEGER PRIMARY KEY,
  user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key_id   INTEGER NULL REFERENCES api_keys(id) ON DELETE SET NULL,
  key_name TEXT NOT NULL,
  at       TEXT NOT NULL,
  method   TEXT NOT NULL,
  path     TEXT NOT NULL,
  status   INTEGER NOT NULL,
  via      TEXT NOT NULL DEFAULT '',
  client   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX api_key_log_user ON api_key_log(user_id, id);
