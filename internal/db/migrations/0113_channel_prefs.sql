-- «ТВ»: a profile's own choices for its channel list (internal/channels).
-- favorites_first: the starred channels stand at the top of the list (in
-- «Все» and in each category), on the web and in the order GET
-- /api/channels gives the apps; a profile without a row has it on.
CREATE TABLE channel_prefs (
  user_id         INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  favorites_first INTEGER NOT NULL DEFAULT 1
);
