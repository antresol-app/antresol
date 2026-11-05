-- Soft delete: files and items that disappeared from disk (trashed, moved,
-- a disk not mounted) are marked missing instead of deleted, so watch
-- progress, watchlist entries and TMDB metadata survive an undo or a
-- restore from the trash; the next scan that finds the file clears the
-- mark. Rows missing for 30 days are purged.
ALTER TABLE items ADD COLUMN missing_since TEXT NULL;
ALTER TABLE files ADD COLUMN missing_since TEXT NULL;
CREATE INDEX items_missing ON items(missing_since) WHERE missing_since IS NOT NULL;
CREATE INDEX files_missing ON files(missing_since) WHERE missing_since IS NOT NULL;
