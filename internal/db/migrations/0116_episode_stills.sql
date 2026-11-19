-- title_episodes.still: the episode's own picture (TMDB still_path, "" =
-- none), so «Календарь» shows the episode rather than the series' poster.
-- Filled by the normal refresh of a tracked title (no backfill job).
ALTER TABLE title_episodes ADD COLUMN still TEXT NOT NULL DEFAULT '';
