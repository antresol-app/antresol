-- Plan 2, Task 15 review: when the app password was made. The web UI shows
-- the password only once, at creation; afterwards GET reports just whether
-- one exists and since when (RFC 3339, UTC).
ALTER TABLE users ADD COLUMN app_password_created_at TEXT;
