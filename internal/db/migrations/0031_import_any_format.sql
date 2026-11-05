-- An import album no release of «Формат загрузки» holds waits as
-- no_format; «Скачать в другом формате» sets any_format, and its search
-- then takes any format.
ALTER TABLE import_groups ADD COLUMN any_format INTEGER NOT NULL DEFAULT 0;
