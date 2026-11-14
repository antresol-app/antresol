-- Cleaned voice-overs: a stereo voice-over over a 5.1 or 7.1 original is
-- now cleaned (its left and right have the original's centre subtracted).
-- Those measured before as «layouts differ: 2 and 6/8 channels» are
-- measured again.
UPDATE cleaned_audio SET state = 'queued', measure_json = '', retry_at = NULL
 WHERE state = 'not_eligible'
   AND (measure_json LIKE '%layouts differ: 2 and 6 channels%' OR measure_json LIKE '%layouts differ: 2 and 8 channels%');
