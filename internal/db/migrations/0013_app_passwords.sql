-- Plan 2: app passwords for Subsonic clients (Amperfy, play:Sub). Stored in
-- the clear: Subsonic token auth sends md5(password + salt), so the server
-- needs the password itself. It is generated, never chosen by the user.
ALTER TABLE users ADD COLUMN app_password TEXT;
