-- Sign-in on a TV by QR code (auth.Pairings): a device without a keyboard
-- asks for a short code, a signed-in profile approves it on a phone, and the
-- device then gets a native token of that profile (sessions, kind native).
--
-- Only hashes are kept: code_hash is the hex sha256 of the code the TV
-- shows, secret_hash that of the secret only the TV knows (it polls with
-- it). status: pending → approved → done (the token was handed out), or
-- pending → denied; a row past expires_at is dead whatever its status and
-- is deleted an hour later. remote: the TV asked from outside the home
-- network. client: its throttle key (address or /64), for the cap of open
-- requests per client.
CREATE TABLE device_pairings (
  id           INTEGER PRIMARY KEY,
  code_hash    TEXT NOT NULL UNIQUE,
  secret_hash  TEXT NOT NULL UNIQUE,
  device_id    TEXT NOT NULL,
  device_name  TEXT NOT NULL DEFAULT '',
  device_model TEXT NOT NULL DEFAULT '',
  app_version  TEXT NOT NULL DEFAULT '',
  client       TEXT NOT NULL DEFAULT '',
  remote       INTEGER NOT NULL DEFAULT 0,
  status       TEXT NOT NULL DEFAULT 'pending',
  user_id      INTEGER REFERENCES users(id) ON DELETE CASCADE,
  approved_by  INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  decided_at   TEXT
);
CREATE INDEX device_pairings_expires ON device_pairings(expires_at);
CREATE INDEX device_pairings_client ON device_pairings(client, status);
