-- Telegram as a second channel of «Уведомления» (internal/notify).
--
-- notify_prefs: email_on / telegram_on are the channels a profile chose (on
-- by default: an address or a linked chat is what makes a channel work).
-- telegram_chat is the Telegram chat the profile linked with the bot (0:
-- none; one chat belongs to one profile), telegram_name what to call it on
-- screen. tg_*: the Telegram digests' own clock — the last one, and the wait
-- after a failure (as last_sent_at, fail_count and next_try_at for mail).
ALTER TABLE notify_prefs ADD COLUMN email_on INTEGER NOT NULL DEFAULT 1;
ALTER TABLE notify_prefs ADD COLUMN telegram_on INTEGER NOT NULL DEFAULT 1;
ALTER TABLE notify_prefs ADD COLUMN telegram_chat INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notify_prefs ADD COLUMN telegram_name TEXT NOT NULL DEFAULT '';
ALTER TABLE notify_prefs ADD COLUMN tg_last_sent_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notify_prefs ADD COLUMN tg_fail_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE notify_prefs ADD COLUMN tg_next_try_at INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX notify_prefs_telegram_chat ON notify_prefs(telegram_chat) WHERE telegram_chat <> 0;

-- notify_events: sent_at and sending_at belong to the mail channel, these two
-- to Telegram (sending_at: a send that never finished counts as sent after
-- ten minutes). A channel a profile does not use is done from the start, so
-- what was queued for mail alone is done for Telegram.
ALTER TABLE notify_events ADD COLUMN tg_sending_at INTEGER NULL;
ALTER TABLE notify_events ADD COLUMN tg_sent_at INTEGER NULL;
UPDATE notify_events SET tg_sent_at = COALESCE(sent_at, created_at);
CREATE INDEX notify_events_tg_pending ON notify_events(user_id) WHERE tg_sent_at IS NULL;

-- notify_telegram_codes: the one-time code behind «Подключить Telegram»
-- (the link t.me/<bot>?start=<code>): a hash, never the code; one live code
-- per profile, a new one replaces it. lang: the language the profile's
-- screen was in, for the confirmation.
CREATE TABLE notify_telegram_codes (
  code_hash  TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  lang       TEXT NOT NULL DEFAULT '',
  expires_at INTEGER NOT NULL
);
