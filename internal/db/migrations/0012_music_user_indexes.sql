-- Child-side indexes of 0011: play counts and top tracks look plays up by
-- item, and ON DELETE CASCADE from items needs them to avoid full scans.
CREATE INDEX plays_item ON plays(item_id);
CREATE INDEX stars_item ON stars(item_id);
CREATE INDEX playlist_items_item ON playlist_items(item_id);
