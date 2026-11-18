-- «Франшизы»: the franchises of the library's titles that franchise
-- channels play (internal/channels, franchise*.go), worked out in the
-- background from TMDB collections and, with a model, by the AI helper.
-- key: "c<the smallest TMDB collection id of the franchise>"; source: tmdb
-- (a collection in release order) or ai; items_json: the computed order
-- (library item ids); manual_json: the admin's {"order":[ids],"hidden":[ids]}
-- ('' = none), kept across recomputes; input_hash: what the model was given
-- (unchanged: it is not asked again).
CREATE TABLE franchises (
  key         TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  source      TEXT NOT NULL,
  items_json  TEXT NOT NULL,
  manual_json TEXT NOT NULL DEFAULT '',
  input_hash  TEXT NOT NULL DEFAULT '',
  error       TEXT NOT NULL DEFAULT '',
  updated_at  INTEGER NOT NULL
);

-- the last pass: the library's fingerprint it was made for, when, and why it failed
CREATE TABLE franchise_state (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);

-- the model's failures on a cluster of collections (key: the cluster's, as in
-- franchises): it is not asked again before retry_at (exponential, a day at most).
-- model: the identity the failures are of (another model starts afresh).
CREATE TABLE franchise_asks (
  key      TEXT PRIMARY KEY,
  model    TEXT NOT NULL,
  failures INTEGER NOT NULL,
  retry_at INTEGER NOT NULL,
  error    TEXT NOT NULL DEFAULT ''
);

-- Wikidata's franchise facts of a title by its TMDB id (kind movie | tv;
-- data '' = no item), refreshed after 30 days.
CREATE TABLE wikidata_facts (
  kind       TEXT NOT NULL,
  tmdb_id    INTEGER NOT NULL,
  data       TEXT NOT NULL,
  fetched_at INTEGER NOT NULL,
  PRIMARY KEY (kind, tmdb_id)
);
