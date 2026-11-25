-- Service profiles: a profile with users.service = 1 is a machine's, not a
-- person's (the server's own post-deploy playback check signs in as one).
-- It is left out of every list of people (the sign-in picker, the family
-- views, the Jellyfin and Subsonic user lists, the devices of all profiles)
-- and it writes no history: a service profile plays like any other, and the
-- progress, marks, played time and last channel it reports are dropped
-- silently, by the triggers below (RAISE(IGNORE) skips the row, never fails
-- the statement). Created and removed only by `vault service-profile`.
--
-- A new table that holds what a profile watched or listened to needs the same
-- trigger (TestServiceProfileHistoryTables lists every table with a user_id
-- and fails on one nobody has decided about).
ALTER TABLE users ADD COLUMN service INTEGER NOT NULL DEFAULT 0;

CREATE TRIGGER progress_service_silent BEFORE INSERT ON progress
  WHEN (SELECT service FROM users WHERE id = NEW.user_id) = 1
BEGIN SELECT RAISE(IGNORE); END;

CREATE TRIGGER progress_cleared_service_silent BEFORE INSERT ON progress_cleared
  WHEN (SELECT service FROM users WHERE id = NEW.user_id) = 1
BEGIN SELECT RAISE(IGNORE); END;

CREATE TRIGGER watchlist_service_silent BEFORE INSERT ON watchlist
  WHEN (SELECT service FROM users WHERE id = NEW.user_id) = 1
BEGIN SELECT RAISE(IGNORE); END;

CREATE TRIGGER annotations_service_silent BEFORE INSERT ON annotations
  WHEN (SELECT service FROM users WHERE id = NEW.user_id) = 1
BEGIN SELECT RAISE(IGNORE); END;

CREATE TRIGGER watch_time_service_silent BEFORE INSERT ON watch_time
  WHEN (SELECT service FROM users WHERE id = NEW.user_id) = 1
BEGIN SELECT RAISE(IGNORE); END;

CREATE TRIGGER plays_service_silent BEFORE INSERT ON plays
  WHEN (SELECT service FROM users WHERE id = NEW.user_id) = 1
BEGIN SELECT RAISE(IGNORE); END;

CREATE TRIGGER screen_time_usage_service_silent BEFORE INSERT ON screen_time_usage
  WHEN (SELECT service FROM users WHERE id = NEW.user_id) = 1
BEGIN SELECT RAISE(IGNORE); END;

CREATE TRIGGER channel_last_service_silent BEFORE INSERT ON channel_last
  WHEN (SELECT service FROM users WHERE id = NEW.user_id) = 1
BEGIN SELECT RAISE(IGNORE); END;

CREATE TRIGGER title_marks_service_silent BEFORE INSERT ON title_marks
  WHEN (SELECT service FROM users WHERE id = NEW.user_id) = 1
BEGIN SELECT RAISE(IGNORE); END;
