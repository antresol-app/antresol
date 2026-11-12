-- «ТВ» round 5: a channel's uploaded logo (<data>/channel-logos/<id>.png).
-- logo_version is the upload's version, in the logo's URL so caches see a
-- new one at once; 0: none (the generated tile stands for the channel).
ALTER TABLE channels ADD COLUMN logo_version INTEGER NOT NULL DEFAULT 0;
