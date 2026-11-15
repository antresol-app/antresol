package voclean

import (
	"context"
	"time"
)

// Status is the admin's view of the cleaned voice-overs: tracks cleaned,
// due (waiting to be measured or cleaned, retries included), failed
// (retried later or given up) and not eligible.
type Status struct {
	Done        int `json:"done"`
	Queued      int `json:"queued"`
	Failed      int `json:"failed"`
	NotEligible int `json:"notEligible"`
}

// Status counts the tracks by state. Queued is what the last queue read
// found due (tracks never measured included); before the first pass, the
// rows queued or due for a retry.
func (w *Worker) Status(ctx context.Context) (Status, error) {
	var st Status
	if w.DB == nil {
		return st, nil
	}
	now := w.now().UTC().Format(time.RFC3339)
	var rowsDue int
	err := w.DB.QueryRowContext(ctx, `SELECT
		  COALESCE(SUM(state='done'),0), COALESCE(SUM(state='failed'),0), COALESCE(SUM(state='not_eligible'),0),
		  COALESCE(SUM(state='queued' OR (state='failed' AND retry_at IS NOT NULL AND retry_at<=?)),0)
		FROM cleaned_audio`, now).Scan(&st.Done, &st.Failed, &st.NotEligible, &rowsDue)
	if err != nil {
		return st, err
	}
	st.Queued = rowsDue
	if w.dueKnown.Load() {
		st.Queued = int(w.due.Load())
	}
	return st, nil
}

// Recheck is «Перепроверить неподходящие»: the tracks found not eligible
// and the failed ones given up on are measured again (queued, failures
// forgotten), and a pass is asked for. It returns how many were reset.
func (w *Worker) Recheck(ctx context.Context) (int, error) {
	if w.DB == nil {
		return 0, nil
	}
	res, err := w.DB.ExecContext(ctx, `UPDATE cleaned_audio SET state='queued', measure_json='', path='', fails=0, retry_at=NULL, updated_at=?
		WHERE state='not_eligible' OR (state='failed' AND retry_at IS NULL)`, w.stamp())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n > 0 && w.dueKnown.Load() {
		w.due.Add(n)
	}
	w.Kick()
	return int(n), nil
}
