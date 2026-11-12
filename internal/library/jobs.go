package library

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Enricher fills metadata after a scan (tmdb.Enricher, MusicBrainz, ...).
type Enricher interface {
	EnrichPending(ctx context.Context) error
}

// ProbeFunc inspects a media file and returns the result to cache in
// files.probe_json together with its duration.
type ProbeFunc func(ctx context.Context, path string) (probeJSON string, durationMs int64, err error)

// JobStatus is the state of the background library job.
type JobStatus struct {
	Running    bool      `json:"running"`
	Queued     bool      `json:"queued"`
	StartedAt  time.Time `json:"startedAt,omitzero"`
	FinishedAt time.Time `json:"finishedAt,omitzero"`
	Error      string    `json:"error,omitempty"`
	Movies     int       `json:"movies"`
	Episodes   int       `json:"episodes"`
	Albums     int       `json:"albums"`
	Tracks     int       `json:"tracks"`
	Books      int       `json:"books"`
	Audiobooks int       `json:"audiobooks"`
	Comics     int       `json:"comics"` // comic volumes
	Files      int       `json:"files"`
	Skipped    int       `json:"skipped"`
	Removed    int       `json:"removed"`
}

// Jobs runs the library scan (Scanner.ScanAll) periodically (Interval,
// default 15 min) and on request; after every scan it runs the enrichers in
// order and probes new files. SlowEnrichers (rate-limited network sources:
// MusicBrainz, Open Library) run in Run's own background worker instead,
// woken after every scan and every Interval, so a long pass never holds
// back scans, Enrichers or probing.
type Jobs struct {
	Scanner       *Scanner
	Enrichers     []Enricher // optional; nil entries are skipped
	SlowEnrichers []Enricher // optional; run by Run's worker, one pass at a time
	Probe         ProbeFunc  // optional: background probe of files without probe_json
	Interval      time.Duration
	Log           *slog.Logger
	// AfterScan (optional) runs after each scan and its fast enrichers,
	// when new items have their TMDB ids.
	AfterScan func(context.Context)
	// AfterProbe (optional) runs after each run's probe pass: the new
	// files' durations are known by then.
	AfterProbe func(context.Context)

	once      sync.Once
	trigger   chan struct{}
	enrich    chan struct{} // wakes the slow-enricher worker (coalesced)
	mu        sync.Mutex
	status    JobStatus
	badProbes map[int64]bool // files ffprobe failed on; retried after a restart
}

func (j *Jobs) init() {
	j.once.Do(func() {
		j.trigger = make(chan struct{}, 1)
		j.enrich = make(chan struct{}, 1)
		j.badProbes = map[int64]bool{}
		if j.Interval <= 0 {
			j.Interval = 15 * time.Minute
		}
		if j.Log == nil {
			j.Log = slog.Default()
		}
	})
}

// Trigger requests a scan as soon as possible. Requests made while a scan is
// running are coalesced into one follow-up run.
func (j *Jobs) Trigger() {
	j.init()
	j.mu.Lock()
	defer j.mu.Unlock()
	select {
	case j.trigger <- struct{}{}:
		j.status.Queued = true
	default:
	}
}

// Status returns a snapshot of the job state.
func (j *Jobs) Status() JobStatus {
	j.init()
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

// Run scans immediately, then on every tick or Trigger, until ctx is done;
// it returns once the slow-enricher worker has stopped too.
func (j *Jobs) Run(ctx context.Context) {
	j.init()
	if len(j.SlowEnrichers) > 0 {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); j.slowLoop(ctx) }()
		defer wg.Wait()
	}
	t := time.NewTicker(j.Interval)
	defer t.Stop()
	for {
		j.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-j.trigger:
		}
	}
}

// RunOnce performs one scan of all roots + enrichment + probe pass.
func (j *Jobs) RunOnce(ctx context.Context) {
	j.init()
	j.mu.Lock()
	select { // a pending request is satisfied by this run
	case <-j.trigger:
	default:
	}
	j.status.Running, j.status.Queued = true, false
	j.status.StartedAt = time.Now().UTC()
	j.mu.Unlock()

	var errMsg string
	st, err := j.Scanner.ScanAll(ctx)
	if err != nil {
		errMsg = err.Error()
		j.Log.Warn("library: scan", "err", err)
	}
	select { // wake the slow enrichers; never wait for them
	case j.enrich <- struct{}{}:
	default:
	}
	j.runEnrichers(ctx, j.Enrichers)
	if j.AfterScan != nil && ctx.Err() == nil {
		j.AfterScan(ctx)
	}
	if j.Probe != nil && ctx.Err() == nil {
		j.probePending(ctx)
	}
	if j.AfterProbe != nil && ctx.Err() == nil {
		j.AfterProbe(ctx)
	}

	j.mu.Lock()
	j.status.Running = false
	j.status.FinishedAt = time.Now().UTC()
	j.status.Error = errMsg
	j.status.Movies, j.status.Episodes, j.status.Files = st.Movies, st.Episodes, st.Files
	j.status.Albums, j.status.Tracks, j.status.Books = st.Albums, st.Tracks, st.Books
	j.status.Audiobooks, j.status.Comics = st.Audiobooks, st.Comics
	j.status.Skipped, j.status.Removed = st.Skipped, st.Removed
	j.mu.Unlock()
}

func (j *Jobs) runEnrichers(ctx context.Context, list []Enricher) {
	for _, en := range list {
		if en == nil || ctx.Err() != nil {
			continue
		}
		if err := en.EnrichPending(ctx); err != nil && ctx.Err() == nil {
			j.Log.Warn("library: enrich", "enricher", fmt.Sprintf("%T", en), "err", err)
		}
	}
}

// slowLoop runs SlowEnrichers after each scan's wake-up and every Interval
// until ctx is done (a running pass stops at its next request).
func (j *Jobs) slowLoop(ctx context.Context) {
	t := time.NewTicker(j.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-j.enrich:
		case <-t.C:
		}
		j.runEnrichers(ctx, j.SlowEnrichers)
	}
}

// probeBatch bounds how many unprobed files one query loads.
const probeBatch = 500

// probePending probes the files without a cached probe, videos first, in
// batches. A Trigger (a finished download) ends the pass after the current
// file: the follow-up run scans and enriches the new title first, then
// carries on here. Files ffprobe failed on are skipped until a restart.
func (j *Jobs) probePending(ctx context.Context) {
	for {
		j.mu.Lock()
		nBad := len(j.badProbes)
		j.mu.Unlock()
		// the bad ones stay unprobed, so the batch makes room for them
		files, err := j.Scanner.Repo.FilesWithoutProbe(ctx, probeBatch+nBad)
		if err != nil {
			j.Log.Warn("library: probe list", "err", err)
			return
		}
		progressed := false
		for _, f := range files {
			if ctx.Err() != nil {
				return
			}
			j.mu.Lock()
			bad := j.badProbes[f.ID]
			j.mu.Unlock()
			if bad {
				continue
			}
			progressed = true
			js, dur, err := j.Probe(ctx, f.Path)
			if err == nil {
				err = j.Scanner.Repo.SetProbe(ctx, f.ID, js, dur)
			}
			if err != nil && ctx.Err() == nil {
				j.Log.Warn("library: probe", "file", f.Path, "err", err)
				j.mu.Lock()
				j.badProbes[f.ID] = true
				j.mu.Unlock()
			}
			if len(j.trigger) > 0 {
				return
			}
		}
		if !progressed || len(files) < probeBatch+nBad {
			return
		}
	}
}
