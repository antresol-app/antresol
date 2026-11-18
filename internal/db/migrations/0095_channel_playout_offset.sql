-- channel_playout offset_ms: where in the file a row starts (ms). A channel
-- that skips openings and credits (rules skipSegments) plays an episode as
-- pieces of its file: [0, intro) and [intro end, credits). 0: from the
-- start, as every row before this migration.
ALTER TABLE channel_playout ADD COLUMN offset_ms INTEGER NOT NULL DEFAULT 0;
