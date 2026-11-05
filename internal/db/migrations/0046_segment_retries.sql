-- Failed steps of the skip-segment queue are retried with a backoff (1 h
-- doubling to 7 days) instead of being stored as done: a chapter read that
-- timed out, an online answer that was not understood, a fingerprint that
-- failed. *_fails counts the failures in a row, *_retry is when (unix
-- seconds) the step is due again.
ALTER TABLE segment_status ADD COLUMN chapters_fails INTEGER NOT NULL DEFAULT 0;
ALTER TABLE segment_status ADD COLUMN chapters_retry INTEGER NOT NULL DEFAULT 0;
ALTER TABLE segment_status ADD COLUMN online_fails INTEGER NOT NULL DEFAULT 0;
ALTER TABLE segment_status ADD COLUMN online_retry INTEGER NOT NULL DEFAULT 0;
ALTER TABLE segment_status ADD COLUMN analysis_fails INTEGER NOT NULL DEFAULT 0;
ALTER TABLE segment_status ADD COLUMN analysis_retry INTEGER NOT NULL DEFAULT 0;
