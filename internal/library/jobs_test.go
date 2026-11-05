package library

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type countEnricher struct{ n atomic.Int32 }

func (c *countEnricher) EnrichPending(context.Context) error { c.n.Add(1); return nil }

func TestJobsRunOnceScanEnrichProbe(t *testing.T) {
	s, mv, _ := newScanner(t)
	touch(t, filepath.Join(mv, "Брат (1997)", "Брат (1997).mkv"), "x")
	touch(t, filepath.Join(mv, "Брат 2 (2000)", "Брат 2 (2000).mkv"), "x")
	en := &countEnricher{}
	probes := 0
	j := &Jobs{Scanner: s, Enrichers: []Enricher{en}, Probe: func(_ context.Context, p string) (string, int64, error) {
		probes++
		if filepath.Base(p) == "Брат 2 (2000).mkv" {
			return "", 0, errors.New("broken")
		}
		return `{"Container":"mkv"}`, 1000, nil
	}}
	ctx := context.Background()
	j.RunOnce(ctx)
	st := j.Status()
	if st.Running || st.Movies != 2 || st.FinishedAt.IsZero() || en.runs() != 1 || probes != 2 {
		t.Fatalf("%+v enrich=%d probes=%d", st, en.runs(), probes)
	}
	left, _ := s.Repo.FilesWithoutProbe(ctx, 0)
	if len(left) != 1 {
		t.Fatalf("left %v", left)
	}
	j.RunOnce(ctx) // the broken file is not retried in this process
	if probes != 2 {
		t.Fatalf("probes=%d", probes)
	}
}

func TestJobsTriggerCoalesces(t *testing.T) {
	s, _, _ := newScanner(t)
	en := &countEnricher{}
	j := &Jobs{Scanner: s, Enrichers: []Enricher{en}, Interval: time.Hour}
	j.Trigger()
	j.Trigger() // coalesced
	if !j.Status().Queued {
		t.Fatal("queued")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { j.Run(ctx); close(done) }()
	wait := func(n int) {
		deadline := time.Now().Add(5 * time.Second)
		for (en.runs() < n || j.Status().Running) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait(1) // the initial run satisfies the queued requests
	time.Sleep(50 * time.Millisecond)
	if n := en.runs(); n != 1 || j.Status().Queued {
		t.Fatalf("runs=%d %+v", n, j.Status())
	}
	j.Trigger()
	wait(2)
	cancel()
	<-done
	if n := en.runs(); n != 2 {
		t.Fatalf("runs=%d", n)
	}
}

func (c *countEnricher) runs() int { return int(c.n.Load()) }

type namedEnricher struct {
	name  string
	order *[]string
}

func (n namedEnricher) EnrichPending(context.Context) error {
	*n.order = append(*n.order, n.name)
	return errors.New("offline") // an error does not stop the next enricher
}

func TestJobsMultipleEnrichers(t *testing.T) {
	s, _, _ := newScanner(t)
	var order []string
	j := &Jobs{Scanner: s, Enrichers: []Enricher{namedEnricher{"tmdb", &order}, nil, namedEnricher{"musicbrainz", &order}}}
	j.RunOnce(context.Background())
	if len(order) != 2 || order[0] != "tmdb" || order[1] != "musicbrainz" {
		t.Fatalf("order %v", order)
	}
}

func TestJobsRunsAllScanners(t *testing.T) {
	s, _, _ := newScanner(t)
	third := false
	s.extraOverride = []scanFunc{
		{Name: "music", Run: func(_ context.Context, st *ScanStats) error { st.Albums++; st.Tracks += 2; return nil }},
		{Name: "books", Run: func(_ context.Context, st *ScanStats) error { st.Books += 3; return errors.New("broken opf") }},
		{Name: "comics", Run: func(_ context.Context, st *ScanStats) error {
			third = true
			st.Audiobooks, st.Comics = 4, 5
			st.Files += 7
			return nil
		}},
	}
	j := &Jobs{Scanner: s}
	j.RunOnce(context.Background())
	st := j.Status()
	if !third {
		t.Fatal("third scanner not run")
	}
	if !strings.Contains(st.Error, "books: broken opf") || strings.Contains(st.Error, "music") {
		t.Fatalf("error %q", st.Error)
	}
	if st.Albums != 1 || st.Tracks != 2 || st.Books != 3 || st.Audiobooks != 4 || st.Comics != 5 || st.Files != 7 {
		t.Fatalf("%+v", st)
	}
}

func TestProbeSkipsBooksAndArchives(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	book := insItem(t, r, KindBook, 0, "Книга", "")
	vol := insItem(t, r, KindComicVolume, 0, "Том", "")
	track := insItem(t, r, KindTrack, 0, "Трек", "")
	movie := insItem(t, r, KindMovie, 0, "Фильм", "")
	insFile(t, r, book, "/m/books/ebooks/Книга.epub")
	insFile(t, r, vol, "/m/books/manga/Серия/Том 1.cbz")
	insFile(t, r, vol, "/m/books/manga/Серия/Том 2") // a folder of images
	insFile(t, r, track, "/m/music/A/B/01.FLAC")
	insFile(t, r, movie, "/m/movies/Фильм/Фильм.mkv")
	var probed []string
	j := &Jobs{Scanner: &Scanner{Repo: r}, Probe: func(_ context.Context, p string) (string, int64, error) {
		probed = append(probed, filepath.Base(p))
		return `{}`, 1, nil
	}}
	j.RunOnce(ctx)
	if len(probed) != 2 || probed[0] != "Фильм.mkv" || probed[1] != "01.FLAC" { // video first
		t.Fatalf("probed %v", probed)
	}
}

// F14: a long probe pass over a big music import must not hold back a new
// movie: videos go first, and a Trigger (a finished download) ends the
// pass after the current file so the next run scans and enriches first.
func TestProbeVideoFirstAndYieldsToTrigger(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	for i := 1; i <= 3; i++ {
		tr := insItem(t, r, KindTrack, 0, "Трек", "")
		insFile(t, r, tr, "/m/music/A/B/0"+string(rune('0'+i))+".flac")
	}
	movie := insItem(t, r, KindMovie, 0, "Фильм", "")
	insFile(t, r, movie, "/m/movies/Фильм/Фильм.mkv")
	var probed []string
	var j *Jobs
	j = &Jobs{Scanner: &Scanner{Repo: r}, Probe: func(_ context.Context, p string) (string, int64, error) {
		probed = append(probed, filepath.Base(p))
		if len(probed) == 2 {
			j.Trigger() // a download finished meanwhile
		}
		return `{}`, 1, nil
	}}
	j.RunOnce(ctx)
	if strings.Join(probed, " ") != "Фильм.mkv 01.flac" || !j.Status().Queued {
		t.Fatalf("first pass probed %v, queued %v", probed, j.Status().Queued)
	}
	j.RunOnce(ctx) // the follow-up run carries on
	if strings.Join(probed, " ") != "Фильм.mkv 01.flac 02.flac 03.flac" {
		t.Fatalf("probed %v", probed)
	}
}

// AfterScan runs once per run, after the scan and the fast enrichers.
func TestJobsAfterScanRunsAfterEnrichers(t *testing.T) {
	s, _, _ := newScanner(t)
	en := &countEnricher{}
	var after []int
	j := &Jobs{Scanner: s, Enrichers: []Enricher{en}, AfterScan: func(context.Context) { after = append(after, en.runs()) }}
	j.RunOnce(context.Background())
	j.RunOnce(context.Background())
	if len(after) != 2 || after[0] != 1 || after[1] != 2 {
		t.Fatalf("%v", after)
	}
}
