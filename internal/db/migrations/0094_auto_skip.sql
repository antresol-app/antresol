-- playback_flags auto-skip: the profile skips the recap («Ранее в
-- сериале»), the opening titles and the end credits by itself, on every
-- device and on TV channels. No row or 0: the skip button only.
ALTER TABLE playback_flags ADD COLUMN skip_recap INTEGER NOT NULL DEFAULT 0;
ALTER TABLE playback_flags ADD COLUMN skip_intro INTEGER NOT NULL DEFAULT 0;
ALTER TABLE playback_flags ADD COLUMN skip_credits INTEGER NOT NULL DEFAULT 0;
