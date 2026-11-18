-- Goodreads shelves of a profile (internal/accounts/goodreads): the books of
-- its library export (CSV) and of the public shelf widget, kept so that a
-- book the library gets later is still found, and so that a shelf's effect
-- on the library's books is applied once, not again at every read.
--
-- entry_key is the normalized title (without its series note) and first
-- author, the same whichever source a row came from. shelf is the book's
-- exclusive Goodreads shelf as last read. read_at and added_at are unix
-- seconds, 0 = unknown. matched is 1 when the last pass found a library
-- book for it; applied_shelf is the shelf whose effect on those books has
-- been applied ('' = none yet): a changed shelf is applied again, an
-- unchanged one never, so a mark the profile removes here stays removed.
CREATE TABLE goodreads_entries (
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  entry_key     TEXT NOT NULL,
  title         TEXT NOT NULL,
  author        TEXT NOT NULL DEFAULT '',
  isbn13        TEXT NOT NULL DEFAULT '',
  shelf         TEXT NOT NULL CHECK (shelf IN ('read','currently-reading','to-read')),
  read_at       INTEGER NOT NULL DEFAULT 0,
  added_at      INTEGER NOT NULL DEFAULT 0,
  matched       INTEGER NOT NULL DEFAULT 0,
  applied_shelf TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (user_id, entry_key)
);
