package myshows

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

type fakeLookup struct {
	calls int
	info  ShowInfo
}

func (f *fakeLookup) Show(_ context.Context, id int64) (ShowInfo, error) {
	f.calls++
	i := f.info
	i.ID = id
	return i, nil
}

func newMapper(t *testing.T, lk *fakeLookup, find func(context.Context, string) (int64, string, bool, error)) (*Mapper, *time.Time, *sql.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	now := time.Unix(2_000_000_000, 0)
	return &Mapper{DB: d, Client: lk, Find: find, Now: func() time.Time { return now }}, &now, d
}

func TestResolveCachesHitAndMiss(t *testing.T) {
	lk := &fakeLookup{info: ShowInfo{IMDbID: 412142}}
	var finds int
	var gotIMDb string
	m, _, _ := newMapper(t, lk, func(_ context.Context, imdb string) (int64, string, bool, error) {
		finds++
		gotIMDb = imdb
		if imdb == "tt0412142" {
			return 77, "series", true, nil
		}
		return 0, "", false, nil
	})
	for i := 0; i < 2; i++ {
		id, kind, ok, err := m.Resolve(context.Background(), 1)
		if err != nil || !ok || id != 77 || kind != "series" {
			t.Fatalf("hit: %d %s %v %v", id, kind, ok, err)
		}
	}
	if gotIMDb != "tt0412142" || lk.calls != 1 || finds != 1 {
		t.Fatalf("imdb=%s show calls=%d finds=%d", gotIMDb, lk.calls, finds)
	}
	lk.info.IMDbID = 9
	for i := 0; i < 2; i++ {
		if _, _, ok, err := m.Resolve(context.Background(), 2); err != nil || ok {
			t.Fatalf("miss: ok=%v err=%v", ok, err)
		}
	}
	if lk.calls != 2 || finds != 2 {
		t.Fatalf("miss not cached: show calls=%d finds=%d", lk.calls, finds)
	}
}

func TestResolveRetriesMissAfter30Days(t *testing.T) {
	lk := &fakeLookup{info: ShowInfo{IMDbID: 9}}
	var finds int
	m, now, _ := newMapper(t, lk, func(context.Context, string) (int64, string, bool, error) {
		finds++
		if finds == 1 {
			return 0, "", false, nil
		}
		return 5, "movie", true, nil
	})
	m.Resolve(context.Background(), 3)
	*now = now.Add(29 * 24 * time.Hour)
	if _, _, ok, _ := m.Resolve(context.Background(), 3); ok || finds != 1 {
		t.Fatalf("retried too early: ok=%v finds=%d", ok, finds)
	}
	*now = now.Add(2 * 24 * time.Hour)
	id, kind, ok, err := m.Resolve(context.Background(), 3)
	if err != nil || !ok || id != 5 || kind != "movie" || finds != 2 {
		t.Fatalf("after 31d: %d %s %v %v finds=%d", id, kind, ok, err, finds)
	}
}

func TestNoIMDbIsMiss(t *testing.T) {
	lk := &fakeLookup{} // IMDbID 0
	m, _, _ := newMapper(t, lk, func(context.Context, string) (int64, string, bool, error) {
		t.Fatal("Find must not run without an IMDb id")
		return 0, "", false, nil
	})
	for i := 0; i < 2; i++ {
		if _, _, ok, err := m.Resolve(context.Background(), 4); err != nil || ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	}
	if lk.calls != 1 {
		t.Fatalf("miss not cached, show calls=%d", lk.calls)
	}
}

func TestFindErrorNotCached(t *testing.T) {
	lk := &fakeLookup{info: ShowInfo{IMDbID: 9}}
	fail := true
	m, now, _ := newMapper(t, lk, func(context.Context, string) (int64, string, bool, error) {
		if fail {
			return 0, "", false, context.DeadlineExceeded
		}
		return 8, "series", true, nil
	})
	if _, _, _, err := m.Resolve(context.Background(), 6); err == nil {
		t.Fatal("want error")
	}
	fail = false
	// not asked again within 6 hours, even though TMDB is back
	if _, _, _, err := m.Resolve(context.Background(), 6); !errors.Is(err, ErrFind) {
		t.Fatalf("want the remembered failure, got %v", err)
	}
	*now = now.Add(findRetry)
	if id, _, ok, err := m.Resolve(context.Background(), 6); err != nil || !ok || id != 8 {
		t.Fatalf("id=%d ok=%v err=%v", id, ok, err)
	}
}

type goneLookup struct{ calls int }

func (g *goneLookup) Show(context.Context, int64) (ShowInfo, error) {
	g.calls++
	return ShowInfo{}, ErrNotFound
}

func TestDeletedShowCachedAsMiss(t *testing.T) {
	g := &goneLookup{}
	m, _, _ := newMapper(t, nil, func(context.Context, string) (int64, string, bool, error) {
		t.Fatal("Find must not run for a deleted show")
		return 0, "", false, nil
	})
	m.Client = g
	for i := 0; i < 2; i++ {
		if _, _, ok, err := m.Resolve(context.Background(), 7); err != nil || ok {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
	}
	if g.calls != 1 {
		t.Fatalf("deleted show re-fetched: calls=%d", g.calls)
	}
}
