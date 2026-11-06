-- Top-level items (parent_id IS NULL: thousands of rows) were looked up
-- through items_parent once the planner's statistics said a parent has a
-- dozen children: every top-level row was read for each lookup by TMDB id
-- (4 ms instead of 0.2 ms for 240 ids, on every downloads page). Without the
-- NULL rows the index serves only lookups by parent, which still use it.
DROP INDEX items_parent;
CREATE INDEX items_parent ON items(parent_id) WHERE parent_id IS NOT NULL;
