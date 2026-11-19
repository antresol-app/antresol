-- What the AI helper («ИИ-помощник», its medium model) decided about the
-- variants of a channel the rules were unsure of (internal/iptv
-- variants_ai.go): a cluster of a playlist's look-alike entries («Россия 1
-- Москва», «Rossiya 1», «Россия 1 Владивосток»), keyed by the hash of the
-- cluster's content (each entry's ident, name, group, tvg-id), so an
-- unchanged playlist is never asked again. answer_json: per entry
-- {"i": ident, "c": channel number within the cluster (0: the AI could not
-- tell, the rules stand), "k": main|quality|regional|timeshift}; model: the
-- model that answered; made_ms: when.
CREATE TABLE iptv_variant_ai (
  cluster_key TEXT PRIMARY KEY,
  answer_json TEXT NOT NULL DEFAULT '[]',
  model       TEXT NOT NULL DEFAULT '',
  made_ms     INTEGER NOT NULL DEFAULT 0
);
