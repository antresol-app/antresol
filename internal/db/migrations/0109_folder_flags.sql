-- The marks of the library folders a title's files are in («Настройки
-- сервера → Медиатека», internal/library folder_flags.go): adult, from a
-- folder «Только для взрослых» (judged as rated 18+: kids never see it);
-- unlisted, from one «Скрыть из общих подборок» (off the home shelves and the
-- generated channels). The scanner keeps them in line with the folders.
ALTER TABLE items ADD COLUMN adult INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN unlisted INTEGER NOT NULL DEFAULT 0;
