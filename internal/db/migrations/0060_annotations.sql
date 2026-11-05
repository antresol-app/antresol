-- Bookmarks, highlights and notes of a profile in a book, and bookmarks in
-- an audiobook (0057–0059 left free for branches in flight).
--
-- id is made by the client (a UUID), so a write queued offline and sent
-- twice lands once. locator is the reading-progress locator: a CFI (a point
-- for a bookmark, a range for a highlight or a note) or "page:N" for PDF
-- and comics; "ms:N" in an audiobook (its whole timeline, as position_ms).
-- fraction orders them through the book. color is one of the four presets
-- of a highlight or a note ('' for a bookmark). label is where it is
-- («Глава 3», «Страница 12»), as the client named it.
--
-- A delete keeps the row as a tombstone (deleted=1, its texts cleared).
-- Order is the server's, never a device clock: every accepted write gets
-- the server's time (updated_at) and the profile's next revision (rev). A
-- write names the revision it was made on (its base): on the latest one it
-- applies; on an older one (a device that was offline) a delete still
-- applies, an edit never brings back a tombstone, and an edit over another
-- edit applies only when its client_at (the device's own time of the
-- change, a hint only) is later.
CREATE TABLE annotations (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  id         TEXT NOT NULL,
  kind       TEXT NOT NULL,
  locator    TEXT NOT NULL,
  fraction   REAL NOT NULL DEFAULT 0,
  label      TEXT NOT NULL DEFAULT '',
  excerpt    TEXT NOT NULL DEFAULT '',
  note       TEXT NOT NULL DEFAULT '',
  color      TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  client_at  TEXT NOT NULL DEFAULT '',
  rev        INTEGER NOT NULL,
  deleted    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (user_id, item_id, id)
);
CREATE INDEX annotations_item ON annotations(item_id);
CREATE INDEX annotations_rev ON annotations(user_id, rev);
