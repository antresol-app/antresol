-- users.kindle_email: the Send to Kindle address of the profile («Отправить
-- на Kindle»), set by an administrator. Empty: the profile has no Kindle.
ALTER TABLE users ADD COLUMN kindle_email TEXT NOT NULL DEFAULT '';
