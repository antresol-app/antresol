-- files.scan_sig: what the video scan read for the file (see movieSig and
-- episodeSig), so a rescan skips a file that did not change. '' = not known.
ALTER TABLE files ADD COLUMN scan_sig TEXT NOT NULL DEFAULT '';

-- The probe pass asks for the files without a probe after every scan, and
-- the scan looks up a series by the ids its tvshow.nfo names; both found
-- their rows by reading the whole table. A file with no probe is NULL
-- (never ''), so the first index holds the files without one: those the pass
-- wants, and the books and archives it never probes, which stay in it.
UPDATE files SET probe_json=NULL WHERE probe_json='';
CREATE INDEX IF NOT EXISTS files_unprobed ON files(id) WHERE probe_json IS NULL AND missing_since IS NULL;
CREATE INDEX IF NOT EXISTS items_series_tvdb ON items(json_extract(meta_json,'$.tvdb_id')) WHERE kind='series';
CREATE INDEX IF NOT EXISTS items_series_imdb ON items(json_extract(meta_json,'$.imdb_id')) WHERE kind='series';
