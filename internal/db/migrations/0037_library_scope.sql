-- «Моя медиатека» (plan: my-library). item_owners: who brought a title
-- into the library. The row is on the title a library page lists: a movie,
-- a series (any season or episode of it), an album, a book, an audiobook
-- or a comic series; an artist is someone's when one of its albums is. A
-- title can have several owners (two profiles asked for seasons of one
-- series); a title nobody asked for (a scan of files put there by hand, a
-- migration) has none and is in the shared library only.
CREATE TABLE item_owners (
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (item_id, user_id)
);
CREATE INDEX item_owners_user ON item_owners(user_id, item_id);

-- users.library_scope: which library the profile's web pages show, «all»
-- (the shared one) or «mine». Only a preference the apps read: the API
-- filters by an explicit ?scope=mine, never by this.
ALTER TABLE users ADD COLUMN library_scope TEXT NOT NULL DEFAULT 'all';
