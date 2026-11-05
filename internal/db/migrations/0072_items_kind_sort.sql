-- Lists in title order (the songs page, artists, titles by name) read the
-- index from the first title of the page on and stop at its end; without it
-- every item of the kind was read and sorted for each page (24 000 tracks
-- for 200 songs).
CREATE INDEX items_kind_sort ON items(kind, sort_title);
