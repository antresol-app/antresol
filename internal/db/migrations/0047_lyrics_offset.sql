-- track_lyrics.offset_ms: the owner's correction of a track's synced
-- words, in milliseconds added to every line time (positive: the lines come
-- later; an LRC «[offset:…]» tag in the text still applies, under it).
-- LRCLIB words are timed to some release, not always to the file in the
-- library. Kept across refetches (SaveTrackLyrics never writes it); a track
-- whose file has synced words gets a row just for it (status error,
-- attempts 0: nothing asked yet). Clamped to ±10 000 by the server.
ALTER TABLE track_lyrics ADD COLUMN offset_ms INTEGER NOT NULL DEFAULT 0;
