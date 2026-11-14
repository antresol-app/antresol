package api

import (
	"bytes"
	"context"
	"testing"

	"github.com/antresol-app/antresol/internal/accounts"
	"github.com/antresol-app/antresol/internal/animemap"
	"github.com/antresol-app/antresol/internal/tracking"
)

// markTracker is the part of accounts.Tracker that MarkChanged reads, over
// the real tracking store.
type markTracker struct {
	accounts.Tracker
	st *tracking.Store
}

func (a markTracker) Title(ctx context.Context, id int64) (accounts.TitleRef, bool, error) {
	t, ok, err := a.st.TitleByID(ctx, id)
	return accounts.TitleRef{ID: t.ID, Kind: t.Kind, TMDBID: t.TMDBID}, ok, err
}

func (a markTracker) Marks(ctx context.Context, uid int64) ([]accounts.Mark, error) {
	ms, err := a.st.Marks(ctx, uid)
	var out []accounts.Mark
	for _, m := range ms {
		out = append(out, accounts.Mark{UserID: m.UserID, TitleID: m.TitleID, State: string(m.State)})
	}
	return out, err
}

// animeSeries says every series is anime-mappable.
type animeSeries struct{ accounts.AnimeMap }

func (animeSeries) HasTMDB(_ context.Context, kind string, _ int64) (bool, error) {
	return kind == "series", nil
}

func (animeSeries) Resolve(context.Context, animemap.Title) (animemap.Entry, bool, error) {
	return animemap.Entry{}, false, nil
}

func TestLocalMarkEnqueuesWhenLinked(t *testing.T) {
	e := newTrackEnv(t)
	ctx := context.Background()
	acc := &accounts.Store{DB: e.db, Key: bytes.Repeat([]byte{3}, 32)}
	sy := &accounts.Syncer{Accounts: acc, Tracking: markTracker{st: e.svc.Store}, Map: animeSeries{}}
	var origins []string
	e.svc.Store.OnMarkChanged = func(ctx context.Context, uid, tid int64, origin string) {
		origins = append(origins, origin)
		sy.MarkChanged(ctx, uid, tid, origin)
	}
	count := func() int {
		items, err := acc.Outbox(ctx, 1, accounts.Shikimori)
		if err != nil {
			t.Fatal(err)
		}
		return len(items)
	}
	// unlinked profile: nothing queued
	if w := e.do("PUT", "/api/marks/series/42", `{"state":"watching"}`, e.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if n := count(); n != 0 {
		t.Fatalf("unlinked: %d queued", n)
	}
	if err := acc.SetNickname(ctx, 1, accounts.Shikimori, "tester", 7); err != nil {
		t.Fatal(err)
	}
	if w := e.do("PUT", "/api/marks/series/42", `{"state":"completed","score":9}`, e.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if n := count(); n != 1 {
		t.Fatalf("linked: %d queued", n)
	}
	// a remote-origin write is announced as such and queues nothing new
	ti, err := e.svc.Store.EnsureTitle(ctx, "series", 43, "x", "")
	if err != nil {
		t.Fatal(err)
	}
	origins = nil
	if err := e.svc.Store.SetMark(ctx, tracking.Mark{UserID: 1, TitleID: ti.ID, State: tracking.Planned, Origin: "shikimori"}); err != nil {
		t.Fatal(err)
	}
	if len(origins) != 1 || origins[0] != "shikimori" {
		t.Fatalf("origins %v", origins)
	}
	if n := count(); n != 1 {
		t.Fatalf("remote write queued: %d", n)
	}
}
