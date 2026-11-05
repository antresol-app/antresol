-- ReplayGain: «Выравнивание громкости» of music (0063 left free for
-- branches in flight).
--
-- rg_*: the file's own ReplayGain tags (REPLAYGAIN_TRACK_GAIN/PEAK,
-- REPLAYGAIN_ALBUM_GAIN/PEAK, Opus R128_*), in dB relative to the
-- ReplayGain reference (89 dB, -18 LUFS) and linear peaks; NULL = no tag.
-- rg_scanned: the tags were read (by the scanner, or for a row from before
-- it read them by the loudness worker, gently; until then not measured).
--
-- loud_*: the server's own measurement (ffmpeg ebur128) of a track with no
-- track gain tag. loud_state 0 = not yet, 1 = measured (loud_lufs NULL:
-- silence, nothing to level), 2 = given up on after 3 failures (its album's
-- gain comes from the rest); a failure counts loud_fails and waits until
-- loud_retry. loud_album_gain/peak: the album's gain from every track's
-- loudness (tags or measured), written once all of them are known.
ALTER TABLE tracks ADD COLUMN rg_track_gain REAL;
ALTER TABLE tracks ADD COLUMN rg_track_peak REAL;
ALTER TABLE tracks ADD COLUMN rg_album_gain REAL;
ALTER TABLE tracks ADD COLUMN rg_album_peak REAL;
ALTER TABLE tracks ADD COLUMN rg_scanned INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tracks ADD COLUMN loud_state INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tracks ADD COLUMN loud_lufs REAL;
ALTER TABLE tracks ADD COLUMN loud_peak REAL;
ALTER TABLE tracks ADD COLUMN loud_fails INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tracks ADD COLUMN loud_retry TEXT;
ALTER TABLE tracks ADD COLUMN loud_album_gain REAL;
ALTER TABLE tracks ADD COLUMN loud_album_peak REAL;
CREATE INDEX tracks_loud_pending ON tracks(loud_state) WHERE loud_state = 0;

-- a file rewritten in place (new size or mtime): its measurement is stale
CREATE TRIGGER IF NOT EXISTS files_loudness_stale AFTER UPDATE OF size, mtime ON files
  WHEN old.size IS NOT new.size OR old.mtime IS NOT new.mtime
BEGIN
  UPDATE tracks SET loud_state = 0, loud_lufs = NULL, loud_peak = NULL, loud_fails = 0, loud_retry = NULL
    WHERE file_id = new.id;
END;
