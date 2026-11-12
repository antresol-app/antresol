-- The interface language a profile chose for the apps («Язык интерфейса»):
-- ru | en. No row: the profile's language (its audio languages, else the
-- household's), so a Russian profile sees a Russian app on an English
-- phone.
CREATE TABLE ui_prefs (
  user_id    INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  lang       TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
