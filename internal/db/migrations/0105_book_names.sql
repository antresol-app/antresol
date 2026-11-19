-- What books are called in other catalogs (internal/metadata/booknames): the
-- names of a book's work found on Wikidata, Open Library or by the AI
-- helper, so a book listed under its English title (a Goodreads shelf) finds
-- the library's Russian edition and back, without asking the sources again.
--
-- key is the normalized title, author and ISBN-13 the book was looked up by
-- ("title|author|isbn"). names_json is what was found ({"t": titles, "a":
-- author names, "i": ISBN-13s of the work's editions, "w": Open Library
-- works, "s": the sources that answered}; "{}" = nobody knew the book).
-- due_at (unix seconds) is when the answer is old enough to be asked again.
CREATE TABLE book_names (
  key        TEXT PRIMARY KEY,
  names_json TEXT NOT NULL DEFAULT '{}',
  due_at     INTEGER NOT NULL DEFAULT 0
);
