package playlistimport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/events"
)

// waitStatus waits until job id is in status (5 s at most).
func waitStatus(t *testing.T, st *Store, id int64, status string) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		j, err := st.Job(context.Background(), id)
		if err == nil && j.Status == status {
			return j
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %d: %+v %v, want %s", id, j, err, status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The core's own background reader: a reading job at once, filled by the
// read (→ preview), or failed with the read's text; one read at a time per
// profile and service; a cancel stops it.
func TestReadInBackground(t *testing.T) {
	ctx := context.Background()
	st, uid := newTestStore(t)
	svc := &Service{Store: st, Bus: &events.Bus{}}

	// filled
	gate := make(chan struct{})
	id, err := svc.ReadInBackground(ctx, uid, ServiceApple, func(ctx context.Context, jobID int64) (Snapshot, error) {
		<-gate
		return Snapshot{Service: "file", Tracks: []SnapTrack{{Key: "a", Artist: "Кино", Title: "Кукушка", Liked: true}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if j, _ := st.Job(ctx, id); j.Status != StatusReading || j.Service != ServiceApple {
		t.Fatalf("job %+v", j)
	}
	// one at a time per profile and service
	if _, err := svc.ReadInBackground(ctx, uid, ServiceApple, nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("second read: err = %v", err)
	}
	close(gate)
	svc.WaitReads()
	if j := waitStatus(t, st, id, StatusPreview); j.Service != ServiceApple {
		t.Fatalf("the read's service is the job's: %+v", j)
	}
	if n, _ := st.Counts(ctx, id); n.Songs != 1 {
		t.Fatalf("counts %+v", n)
	}
	if err := svc.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}

	// failed with the read's text
	id, err = svc.ReadInBackground(ctx, uid, ServiceApple, func(context.Context, int64) (Snapshot, error) {
		return Snapshot{}, errors.New("Медиатека не прочиталась")
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.WaitReads()
	if j := waitStatus(t, st, id, StatusFailed); j.Error != "Медиатека не прочиталась" {
		t.Fatalf("failed job %+v", j)
	}

	// a cancel stops the read; the job stays cancelled, not failed
	stopped := make(chan error, 1)
	id, err = svc.ReadInBackground(ctx, uid, ServiceApple, func(ctx context.Context, _ int64) (Snapshot, error) {
		<-ctx.Done()
		stopped <- ctx.Err()
		return Snapshot{}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("stopped by %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read goes on after the cancel")
	}
	svc.WaitReads()
	if j, _ := st.Job(ctx, id); j.Status != StatusCancelled || j.Error != "" {
		t.Fatalf("cancelled job %+v", j)
	}
	// the profile may read again
	id, err = svc.ReadInBackground(ctx, uid, ServiceApple, func(context.Context, int64) (Snapshot, error) {
		return Snapshot{Tracks: []SnapTrack{{Key: "b", Artist: "Кино", Title: "Звезда"}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.WaitReads()
	waitStatus(t, st, id, StatusPreview)

	// the addons' reads are stopped too (Reads)
	rec := &stopRec{}
	svc.Reads = rec
	if err := svc.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(rec.ids) != 1 || rec.ids[0] != id {
		t.Fatalf("Reads.Stop %v", rec.ids)
	}
}

type stopRec struct{ ids []int64 }

func (s *stopRec) Stop(id int64) { s.ids = append(s.ids, id) }

// A shutdown (Run's context ends) fails the read: the job is not left
// reading for ever.
func TestReadInBackground_Shutdown(t *testing.T) {
	st, uid := newTestStore(t)
	svc := &Service{Store: st, Bus: &events.Bus{}, Tick: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.Run(ctx); close(done) }()
	waitRunning(t, svc)
	id, err := svc.ReadInBackground(context.Background(), uid, ServiceApple, func(ctx context.Context, _ int64) (Snapshot, error) {
		<-ctx.Done()
		return Snapshot{}, ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-done
	svc.WaitReads()
	if j := waitStatus(t, st, id, StatusFailed); j.Error != MsgError {
		t.Fatalf("job %+v", j)
	}
}

// waitRunning waits until Run has taken its context as the reads' base.
func waitRunning(t *testing.T, s *Service) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.readMu.Lock()
		ok := s.readBase != nil
		s.readMu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A read asked for once Run's context has ended (an upload handler still
// parsing when the shutdown's grace ran out) is refused: no job, and no
// goroutine to race WaitReads or write after the database closes.
func TestReadInBackground_AfterShutdownRefused(t *testing.T) {
	st, uid := newTestStore(t)
	svc := &Service{Store: st, Bus: &events.Bus{}, Tick: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { svc.Run(ctx); close(done) }()
	waitRunning(t, svc)
	cancel()
	<-done
	svc.WaitReads()
	called := false
	if _, err := svc.ReadInBackground(context.Background(), uid, ServiceApple, func(context.Context, int64) (Snapshot, error) {
		called = true
		return Snapshot{}, nil
	}); !errors.Is(err, ErrBusy) {
		t.Fatalf("err %v (want ErrBusy)", err)
	}
	svc.WaitReads()
	if called {
		t.Fatal("read ran after the shutdown")
	}
	if ids, err := st.JobIDs(context.Background(), StatusReading); err != nil || len(ids) != 0 {
		t.Fatalf("reading jobs %v %v", ids, err)
	}
}
