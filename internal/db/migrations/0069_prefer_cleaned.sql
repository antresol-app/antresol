-- Cleaned voice-overs in the players (stage C of the voice-over design).
--
-- playback_flags: a profile's switches that are not its «Звук и субтитры»
-- defaults, so changing one never makes those the profile's own (a
-- playback_prefs row detaches the profile from the household «Языки») and
-- «Как у семьи» (deleting that row) keeps them. prefer_cleaned: «Выбирать
-- очищенную озвучку» — the cleaned version (cleaned_audio) of the track
-- the player would have chosen. No row: all off.
CREATE TABLE playback_flags (
  user_id        INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  prefer_cleaned INTEGER NOT NULL DEFAULT 0
);

-- series_tracks.audio_kind: the remembered voice was its cleaned version
-- ('cleaned') or the track itself (''), so a cleaned track picked by hand
-- is picked again in the next episode.
ALTER TABLE series_tracks ADD COLUMN audio_kind TEXT NOT NULL DEFAULT '';
