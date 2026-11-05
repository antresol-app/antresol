CREATE TABLE app_tokens(token TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, proto TEXT NOT NULL, device_id TEXT NOT NULL DEFAULT '', device TEXT NOT NULL DEFAULT '', client TEXT NOT NULL DEFAULT '', version TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, last_seen TEXT NOT NULL);
CREATE INDEX app_tokens_user ON app_tokens(user_id);
