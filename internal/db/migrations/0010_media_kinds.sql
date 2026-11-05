-- Plan 2: music, books, audiobooks and comics.
-- match_key identifies album/artist/book/audiobook/comic items across
-- rescans (formats: internal/library/model.go); NULL for the rest.
ALTER TABLE items ADD COLUMN match_key TEXT;
CREATE INDEX items_match ON items(kind, match_key);

-- A track is an item; its audio is a files row. A plain track owns its
-- file; the tracks of a CUE image all point at the album's one file.
CREATE TABLE tracks (
  item_id  INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  file_id  INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  artist   TEXT NOT NULL DEFAULT '',
  disc_no  INTEGER NOT NULL DEFAULT 1,
  track_no INTEGER NOT NULL DEFAULT 0,
  start_ms INTEGER NOT NULL DEFAULT 0,
  end_ms   INTEGER, -- NULL = to the end of the file
  codec    TEXT NOT NULL DEFAULT '',
  lossless INTEGER NOT NULL DEFAULT 0,
  mbid     TEXT NOT NULL DEFAULT '',
  lyrics   TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX tracks_file_start ON tracks(file_id, start_ms);

-- Audiobook chapters (positions within their file).
CREATE TABLE chapters (
  item_id  INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  idx      INTEGER NOT NULL,
  title    TEXT NOT NULL,
  file_id  INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  start_ms INTEGER NOT NULL DEFAULT 0, -- within the file
  end_ms   INTEGER, -- NULL = the end of the file
  PRIMARY KEY (item_id, idx)
);

-- Reading position: CFI (EPUB/FB2) or "page:N" (PDF, comics).
ALTER TABLE progress ADD COLUMN locator TEXT NOT NULL DEFAULT '';
