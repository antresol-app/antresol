-- Cleaned voice-overs: a stereo voice-over that is mono in both channels
-- (TV voice-overs often are) is no longer refused by the side check
-- (internal/voclean monoSide). Those refused by it since the stereo ones
-- were measured again (0084) are measured once more.
UPDATE cleaned_audio SET state = 'queued', measure_json = '', retry_at = NULL
 WHERE state = 'not_eligible'
   AND measure_json LIKE '%front residual%'
   AND updated_at >= '2026-11-14';
