-- An import album's own download that makes no headway is given up.
-- run_s: how long it has been running (downloading or fetching its
-- metadata while the server is up; paused, held or the server down does
-- not count); quiet_s: of that, since its bytes (wait_done) last grew;
-- run_base: its bytes when run_s started. A dead one (no byte for 1 h, no
-- progress for 6 h) is removed with its files and listed in dead_json:
-- the album's next search leaves it out. A slow one (12 h running, more
-- than 3 days to go) is removed keeping its files and listed in
-- slow_json: taken again only when no other release holds the songs.
ALTER TABLE import_groups ADD COLUMN dead_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE import_groups ADD COLUMN slow_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE import_groups ADD COLUMN run_s INTEGER NOT NULL DEFAULT 0;
ALTER TABLE import_groups ADD COLUMN quiet_s INTEGER NOT NULL DEFAULT 0;
ALTER TABLE import_groups ADD COLUMN run_base INTEGER NULL;
-- match_ver: the matcher version that last decided the song; songs in
-- review of a running job decided by an older one are matched again
-- (once) by the current matcher. A song the admin skipped stays skipped.
ALTER TABLE import_tracks ADD COLUMN match_ver INTEGER NOT NULL DEFAULT 0;
-- A file list that did not load was taken for «no release in the format»
-- (fixed with this version): the running jobs' no_format albums search
-- once more.
UPDATE import_tracks SET state = 'matched', reason = '' WHERE state = 'not_found' AND group_id IN (
  SELECT g.id FROM import_groups g JOIN import_jobs j ON j.id = g.job_id WHERE g.state = 'no_format' AND g.any_format = 0
  AND j.status = 'downloading');
UPDATE import_groups SET state = 'waiting', error = '' WHERE state = 'no_format' AND any_format = 0
  AND job_id IN (SELECT id FROM import_jobs WHERE status = 'downloading');
