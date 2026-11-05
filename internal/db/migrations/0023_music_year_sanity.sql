-- Album and track years from file tags can be garbage (a zero date reads as
-- year 1). Plausible is 1000-2999; anything else means unknown (0). A rescan
-- skips unchanged folders, so the stored values are fixed here.
UPDATE items SET year = 0 WHERE kind IN ('album','track') AND year <> 0 AND (year < 1000 OR year > 2999);
