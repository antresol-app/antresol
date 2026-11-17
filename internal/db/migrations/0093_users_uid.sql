-- users.uid: a profile's stable key for addons. users.id may be given to
-- a new profile once one is deleted (no AUTOINCREMENT); a uid is random,
-- never reused and never changed. Profiles that exist get one now; the
-- server sets one on every profile it creates, and a row inserted
-- without one gets one from users_uid_set.
ALTER TABLE users ADD COLUMN uid TEXT;
UPDATE users SET uid = lower(hex(randomblob(16))) WHERE uid IS NULL;
CREATE UNIQUE INDEX users_uid ON users(uid);
CREATE TRIGGER users_uid_set AFTER INSERT ON users WHEN NEW.uid IS NULL
BEGIN
  UPDATE users SET uid = lower(hex(randomblob(16))) WHERE id = NEW.id;
END;
CREATE TRIGGER users_uid_fixed BEFORE UPDATE OF uid ON users
  WHEN OLD.uid IS NOT NULL AND NEW.uid IS NOT OLD.uid
BEGIN
  SELECT RAISE(ABORT, 'users.uid never changes');
END;
