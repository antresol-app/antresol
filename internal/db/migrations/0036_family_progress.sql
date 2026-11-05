-- «Семья»: adults see the other profiles' progress on an item's page and
-- in the Home shelf «Семья смотрит». hide_progress keeps a profile's own
-- progress out of that view (it still counts for itself and in «Статистика»).
ALTER TABLE users ADD COLUMN hide_progress INTEGER NOT NULL DEFAULT 0;
-- the family view reads every profile's row of one item
CREATE INDEX progress_item ON progress(item_id);
