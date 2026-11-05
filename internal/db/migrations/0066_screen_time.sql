-- Screen time of kid profiles («Экранное время», internal/screentime).
--
-- screen_time_limits: what an admin set for a kid. A NULL minute limit is
-- no limit (audio: music and audiobooks are then not limited at all, not
-- even by the hours); a NULL window is any time of day. Windows are minutes
-- since midnight in the server's time zone (TZ), [from, to); from > to
-- crosses midnight. The screen day (usage, bonus, which window applies)
-- starts at day_start (minutes; default 04:00), so a window like Friday
-- 20:00–02:00 belongs to Friday; a window may not cross the day start.
CREATE TABLE screen_time_limits (
  user_id      INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  video_min    INTEGER NULL,
  audio_min    INTEGER NULL,
  weekday_from INTEGER NULL,
  weekday_to   INTEGER NULL,
  weekend_from INTEGER NULL,
  weekend_to   INTEGER NULL,
  day_start    INTEGER NOT NULL DEFAULT 240,
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- screen_time_bonus: «Бонус на сегодня» — extra minutes of one day.
CREATE TABLE screen_time_bonus (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  day     TEXT NOT NULL, -- YYYY-MM-DD of the screen day (it starts at day_start)
  minutes INTEGER NOT NULL,
  PRIMARY KEY (user_id, day)
) WITHOUT ROWID;

-- screen_time_usage: seconds of real playback per day, category (video |
-- audio) and library item (0: not a library item). No foreign key on the
-- item: the day's total stays when a title is deleted.
CREATE TABLE screen_time_usage (
  user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  day      TEXT NOT NULL,
  category TEXT NOT NULL,
  item_id  INTEGER NOT NULL DEFAULT 0,
  seconds  REAL NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, day, category, item_id)
) WITHOUT ROWID;
