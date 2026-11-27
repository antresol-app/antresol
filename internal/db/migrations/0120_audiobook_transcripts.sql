-- Transcripts and automatic chapters of audiobooks (internal/transcript):
-- made once for a book's files, in pieces, and kept until the files change.
-- Times are within their file; the book's timeline is worked out when read.
CREATE TABLE transcripts (
  item_id        INTEGER PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
  signature      TEXT NOT NULL,  -- the files it was made from: id:size:mtime;…
  state          TEXT NOT NULL DEFAULT 'pending',  -- pending | ready | failed
  language       TEXT NOT NULL DEFAULT '',         -- ISO 639-1
  piece_ms       INTEGER NOT NULL DEFAULT 0,  -- length of a piece; 0: the usual one (shorter for a model without segment times)
  fails          INTEGER NOT NULL DEFAULT 0,
  retry_at       TEXT,           -- failed: when it is tried again (NULL: given up on)
  error          TEXT NOT NULL DEFAULT '',
  chapters_done  INTEGER NOT NULL DEFAULT 0,  -- auto_chapters were made
  chapters_fails INTEGER NOT NULL DEFAULT 0,
  chapters_retry TEXT,
  chapters_error TEXT NOT NULL DEFAULT '',
  updated_at     TEXT NOT NULL
);

-- The finished pieces, so that a restart redoes none of them.
CREATE TABLE transcript_pieces (
  item_id  INTEGER NOT NULL REFERENCES transcripts(item_id) ON DELETE CASCADE,
  file_id  INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  start_ms INTEGER NOT NULL,
  end_ms   INTEGER NOT NULL,
  language TEXT NOT NULL DEFAULT '',
  segments TEXT NOT NULL DEFAULT '[]',  -- JSON [{startMs,endMs,text}] within the file
  PRIMARY KEY (item_id, file_id, start_ms)
);
CREATE INDEX transcript_pieces_file ON transcript_pieces(file_id);

-- Chapters made from a transcript, shown instead of the files' own when
-- those say nothing (the chapters table stays the scanner's).
CREATE TABLE auto_chapters (
  item_id  INTEGER NOT NULL REFERENCES transcripts(item_id) ON DELETE CASCADE,
  idx      INTEGER NOT NULL,
  title    TEXT NOT NULL,
  file_id  INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  start_ms INTEGER NOT NULL,
  PRIMARY KEY (item_id, idx)
);
CREATE INDEX auto_chapters_file ON auto_chapters(file_id);
