-- Cleaned voice-overs of external tracks (stage B's downloads, fitted by
-- internal/vofit): vo_path is the voice-over's file beside the video, its
-- vo_index voclean.ExternalIndex of that path (negative: never a stream
-- of the file); '' for a voice-over stream of the file itself.
ALTER TABLE cleaned_audio ADD COLUMN vo_path TEXT NOT NULL DEFAULT '';
