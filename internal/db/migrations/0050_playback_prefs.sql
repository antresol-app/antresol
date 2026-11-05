-- Audio and subtitle choices that follow a profile across devices
-- (0048, 0049 left free for branches in flight).
--
-- playback_prefs: the profile's defaults («Звук и субтитры»): audio
-- languages best first and subtitle languages (comma-separated lang codes),
-- when subtitles show (off | foreign | always) and how they look in the web
-- player (size s|m|l|xl, background none|translucent|solid). No row: the
-- household «Языки» and the player's old behaviour.
CREATE TABLE playback_prefs (
  user_id        INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  audio_langs    TEXT NOT NULL DEFAULT '',
  sub_mode       TEXT NOT NULL DEFAULT 'off',
  sub_langs      TEXT NOT NULL DEFAULT '',
  sub_size       TEXT NOT NULL DEFAULT 'm',
  sub_background TEXT NOT NULL DEFAULT 'translucent',
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- series_tracks: the audio track (its language tag and title, a dub such as
-- «Кубик в кубе») and the subtitles a profile chose in an episode of a
-- series, applied to its next episodes. sub_state: '' nothing chosen,
-- 'on' (sub_lang/sub_title) or 'off'.
CREATE TABLE series_tracks (
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  series_id   INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  has_audio   INTEGER NOT NULL DEFAULT 0,
  audio_lang  TEXT NOT NULL DEFAULT '',
  audio_title TEXT NOT NULL DEFAULT '',
  sub_state   TEXT NOT NULL DEFAULT '',
  sub_lang    TEXT NOT NULL DEFAULT '',
  sub_title   TEXT NOT NULL DEFAULT '',
  updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY (user_id, series_id)
);
CREATE INDEX series_tracks_series ON series_tracks(series_id);

-- subtitle_offsets: «Субтитры раньше / позже» of a profile for one file, in
-- milliseconds added to every cue time (positive: later). No row: 0.
CREATE TABLE subtitle_offsets (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  file_id    INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  offset_ms  INTEGER NOT NULL,
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  PRIMARY KEY (user_id, file_id)
);
CREATE INDEX subtitle_offsets_file ON subtitle_offsets(file_id);
