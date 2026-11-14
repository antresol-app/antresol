-- Episode source meta and per-profile source choice.
--
-- episode_sources.meta_json: what a source offers beyond provider/quality
-- (audio language, voice, studios, subtitles, ids needed to play it), as the
-- JSON of tracking.SourceMeta.
--
-- title_choices: the source a profile prefers for a title (kind, provider,
-- voice, language, quality; choice_json is tracking.Choice). Keyed by TMDB
-- id, not tracked_titles.id, so a choice survives a title that is no longer
-- tracked.
ALTER TABLE episode_sources ADD COLUMN meta_json TEXT NOT NULL DEFAULT '{}';

CREATE TABLE title_choices (
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL,
  tmdb_id     INTEGER NOT NULL,
  choice_json TEXT NOT NULL,
  updated_at  INTEGER NOT NULL,
  PRIMARY KEY (user_id, kind, tmdb_id)
);
