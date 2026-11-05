-- progress_cleared: «Снять отметку „Просмотрено“» as a state with its own
-- time (a tombstone). The progress row is deleted, and a write older than
-- cleared_at (an offline queue of another device replayed later, its
-- «finished» position included) is ignored instead of bringing the old
-- position or the mark back. A newer write starts the item afresh.
CREATE TABLE progress_cleared (
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  cleared_at TEXT NOT NULL,
  PRIMARY KEY (user_id, item_id)
) WITHOUT ROWID;
CREATE INDEX progress_cleared_item ON progress_cleared(item_id);
