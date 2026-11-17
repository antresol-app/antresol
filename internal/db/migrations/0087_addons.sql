-- Installed addons (internal/addons). token_hash is the sha256 hex of the
-- addon's core-API token; the raw token is never stored.
CREATE TABLE addons (
  id           TEXT PRIMARY KEY,
  source       TEXT NOT NULL,
  version      TEXT NOT NULL,
  prev_version TEXT,
  manifest     TEXT NOT NULL,
  enabled      INTEGER NOT NULL DEFAULT 1,
  token_hash   TEXT NOT NULL,
  installed_at INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
);
CREATE INDEX addons_token_hash ON addons(token_hash);
