-- Managed accounts: a profile row with owner_id set is a history of its
-- own under a person (a shared TV, «Гостиная»). It has no password and is
-- never signed in to; the owner's limits apply to it (read through the
-- owner row, never copied). Deleting the person deletes them.
--
-- users.name is UNIQUE across the table and cannot be scoped without
-- rebuilding users (every history table references it), so a managed
-- account keeps its name in managed_name and an internal key in users.name.
-- The names of the people's profiles stay unique among themselves (users.name),
-- a managed account's name is unique per owner (users_managed_name), and
-- the two never meet: «Гостиная» may be both a profile and a managed
-- account. Both columns are nullable additions: nothing is rewritten.
ALTER TABLE users ADD COLUMN owner_id INTEGER NULL REFERENCES users(id) ON DELETE CASCADE;
ALTER TABLE users ADD COLUMN managed_name TEXT NULL;
CREATE INDEX users_owner ON users(owner_id) WHERE owner_id IS NOT NULL;
CREATE UNIQUE INDEX users_managed_name ON users(owner_id, managed_name) WHERE owner_id IS NOT NULL;

-- device_history: «Отдельная история на этом устройстве». A device of the
-- person user_id writes its history into managed_id while enabled is 1;
-- switched off with «Оставить», the row stays (enabled 0) so switching on
-- again picks the same account. device_key is 'app:' and the native
-- app's install id (it outlives sign-out and sign-in), else 's:' and the
-- session's rowid (a browser: the row goes with the session).
CREATE TABLE device_history (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  device_key TEXT NOT NULL,
  managed_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  enabled    INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY (user_id, device_key)
) WITHOUT ROWID;
CREATE INDEX device_history_managed ON device_history(managed_id);

CREATE TRIGGER sessions_device_history_gone AFTER DELETE ON sessions
  WHEN OLD.kind <> 'native' OR OLD.device_id = ''
BEGIN
  DELETE FROM device_history WHERE user_id = OLD.user_id AND device_key = 's:' || OLD.rowid;
END;
