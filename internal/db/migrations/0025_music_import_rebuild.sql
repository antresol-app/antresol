-- What a library import's rebuild already wrote (plan 6, Task 4 fix):
-- a star is given once per job, so an unstar made during the import
-- stays; a playlist the user changed or deleted during the job that built
-- it is left alone until the next job of that service.
ALTER TABLE import_tracks ADD COLUMN starred INTEGER NOT NULL DEFAULT 0;
ALTER TABLE import_albums ADD COLUMN starred INTEGER NOT NULL DEFAULT 0;
-- built_job: the job whose rebuild last wrote the Vault playlist;
-- built_hash: the fingerprint (name and items) it wrote.
ALTER TABLE import_playlists ADD COLUMN built_job INTEGER NULL;
ALTER TABLE import_playlists ADD COLUMN built_hash TEXT NOT NULL DEFAULT '';
