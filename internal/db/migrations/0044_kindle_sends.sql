-- «На Kindle»: the books a profile has sent, and where. A book counts as sent
-- only to the address it went to: a new address starts over.
CREATE TABLE kindle_sends (
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  address TEXT NOT NULL,
  sent_at INTEGER NOT NULL,
  PRIMARY KEY (user_id, item_id, address)
);
