CREATE TABLE items (
  id             INTEGER PRIMARY KEY,
  kind           TEXT NOT NULL,
  parent_id      INTEGER NULL REFERENCES items(id) ON DELETE CASCADE,
  title          TEXT NOT NULL,
  original_title TEXT NOT NULL DEFAULT '',
  sort_title     TEXT NOT NULL DEFAULT '',
  year           INTEGER NOT NULL DEFAULT 0,
  overview       TEXT NOT NULL DEFAULT '',
  rating         TEXT NOT NULL DEFAULT '',
  tmdb_id        INTEGER NULL,
  season_no      INTEGER NULL,
  episode_no     INTEGER NULL,
  poster         TEXT NULL,
  backdrop       TEXT NULL,
  added_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  updated_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  meta_json      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX items_kind_added ON items(kind, added_at);
CREATE INDEX items_parent ON items(parent_id);
CREATE INDEX items_tmdb ON items(kind, tmdb_id);

CREATE TABLE files (
  id               INTEGER PRIMARY KEY,
  item_id          INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  path             TEXT NOT NULL UNIQUE,
  size             INTEGER NOT NULL DEFAULT 0,
  mtime            INTEGER NOT NULL DEFAULT 0,
  duration_ms      INTEGER NULL,
  probe_json       TEXT NULL,
  torrent_infohash TEXT NULL
);
CREATE INDEX files_item ON files(item_id);

CREATE TABLE item_titles (
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  title   TEXT NOT NULL,
  norm    TEXT NOT NULL,
  PRIMARY KEY (item_id, norm)
);
CREATE INDEX item_titles_norm ON item_titles(norm);

CREATE VIRTUAL TABLE items_fts USING fts5(title, original_title, overview, tokenize='unicode61 remove_diacritics 2');

CREATE TRIGGER items_fts_ai AFTER INSERT ON items BEGIN
  INSERT INTO items_fts(rowid, title, original_title, overview) VALUES (new.id, new.title, new.original_title, new.overview);
END;
CREATE TRIGGER items_fts_au AFTER UPDATE OF title, original_title, overview ON items BEGIN
  DELETE FROM items_fts WHERE rowid = old.id;
  INSERT INTO items_fts(rowid, title, original_title, overview) VALUES (new.id, new.title, new.original_title, new.overview);
END;
CREATE TRIGGER items_fts_ad AFTER DELETE ON items BEGIN
  DELETE FROM items_fts WHERE rowid = old.id;
END;
