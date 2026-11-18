-- A profile's photo, shown in place of the letter on the coloured circle
-- (users.avatar stays: it is the circle's colour and the fallback).
-- user_photos.data is the picture as the server keeps it (a square JPEG,
-- internal/auth/photo.go); users.photo_version names the upload, in the
-- photo's URL so caches see a new one at once; 0: none. The row goes with its
-- profile (a profile id may be given to a new profile once one is deleted,
-- and must not inherit the old face).
ALTER TABLE users ADD COLUMN photo_version INTEGER NOT NULL DEFAULT 0;
CREATE TABLE user_photos (
  user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  data    BLOB NOT NULL
);
