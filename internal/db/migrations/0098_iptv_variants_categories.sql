-- «ТВ» on a big playlist (internal/iptv).
-- preset_variants: a source's variants of one channel (qualities,
-- regional feeds: «Пример HD», «Пример FHD», «Пример +0 (Регион)») are one
-- channel, the best one primary and the others its fallbacks (on by
-- default, as the other presets).
ALTER TABLE iptv_sources ADD COLUMN preset_variants INTEGER NOT NULL DEFAULT 1;

-- What the AI helper («ИИ-помощник», its light model) made of an external
-- channel, cached by its base name (iptv.VariantName): categories
-- (iptv category codes, JSON) and its language (ISO 639-1, '' unknown).
-- One answer serves every source and variant of the channel; made_ms: when.
CREATE TABLE iptv_channel_ai (
  name_key   TEXT PRIMARY KEY,
  cats_json  TEXT NOT NULL DEFAULT '[]',
  lang       TEXT NOT NULL DEFAULT '',
  made_ms    INTEGER NOT NULL DEFAULT 0
);

-- langs_json: the languages a source keeps (ISO 639-1, JSON; [] = all): a
-- channel known to be in another is hidden (iptv languages.go).
ALTER TABLE iptv_sources ADD COLUMN langs_json TEXT NOT NULL DEFAULT '[]';
