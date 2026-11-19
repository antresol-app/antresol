-- A generated channel's logo found by the server itself (a franchise's or
-- a series' title logo, channels/logo_auto.go): 0 = none found or an
-- uploaded one, 1 = the logo was found automatically, 2 = an admin removed
-- the logo (it is not looked for again).
ALTER TABLE channels ADD COLUMN logo_auto INTEGER NOT NULL DEFAULT 0;
