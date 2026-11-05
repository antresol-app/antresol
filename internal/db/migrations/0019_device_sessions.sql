ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT 'web';
ALTER TABLE sessions ADD COLUMN device_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN device_name TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN device_model TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN app_version TEXT NOT NULL DEFAULT '';
CREATE INDEX sessions_device ON sessions(user_id, kind, device_id);
