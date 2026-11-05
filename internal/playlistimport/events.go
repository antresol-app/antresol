package playlistimport

import (
	"context"
	"time"

	"github.com/antresol-app/antresol/internal/events"
)

// progressEvery is the most often import.progress goes out for one job
// (status changes always go out).
const progressEvery = 2 * time.Second

// ProgressEvent is the data of import.progress (admins only).
type ProgressEvent struct {
	JobID    int64    `json:"jobId"`
	Status   string   `json:"status"`
	Progress Progress `json:"progress"`
}

// Publish sends import.progress for a job: always when force (a status
// change), otherwise at most every progressEvery.
func (s *Service) Publish(ctx context.Context, jobID int64, force bool) {
	if s.Bus == nil || !s.Bus.Wants(events.ImportProgress) {
		return
	}
	now := time.Now()
	s.mu.Lock()
	if last, ok := s.published[jobID]; !force && ok && now.Sub(last) < progressEvery {
		s.mu.Unlock()
		return
	}
	if s.published == nil {
		s.published = map[int64]time.Time{}
	}
	s.published[jobID] = now
	s.mu.Unlock()

	j, err := s.Store.Job(ctx, jobID)
	if err != nil {
		return // deleted meanwhile
	}
	p, err := s.Store.Progress(ctx, jobID)
	if err != nil {
		s.logErr(ctx, "progress", jobID, err)
		return
	}
	s.Bus.Publish(events.Event{Type: events.ImportProgress, Data: ProgressEvent{JobID: jobID, Status: j.Status, Progress: p}})
}
