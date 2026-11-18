package addons

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/tracking"
)

// scanTitle is the tracked title the scanner tests scan.
var scanTitle = tracking.Title{ID: 7, Kind: "series", TMDBID: 42, Name: "Тайтл", Status: "airing"}

var scanEpisodes = []tracking.Episode{{TitleID: 7, Season: 1, Number: 1, Name: "Первая", AirDate: "2026-01-02"}}

// oneScanner is the only scanner Scanners gives, of kind.
func oneScanner(t *testing.T, h *Hooks, kind string) tracking.Scanner {
	t.Helper()
	scs := h.Scanners(context.Background())
	if len(scs) != 1 || scs[0].Kind() != kind {
		t.Fatalf("scanners %v", scannerKinds(scs))
	}
	return scs[0]
}

func scannerKinds(scs []tracking.Scanner) []string {
	var out []string
	for _, s := range scs {
		out = append(out, s.Kind())
	}
	return out
}

func TestScanner_Sources(t *testing.T) {
	h, tg := newHooks(t)
	// a source of another kind is taken as the hook's; library ids are not
	// an addon's to give
	hookAddon(t, tg, "fake", `{"tracking.scan:alpha": {"body": {"sources": [
		{"season": 1, "episode": 1, "kind": "online", "ref": "r1", "provider": "p", "quality": "1080p", "labels": ["x"],
		 "meta": {"itemId": 5, "fileId": 6, "sourceId": "s1", "lang": "ru"}},
		{"season": 1, "episode": 2, "kind": "library", "ref": "r2"}]}}}`, []string{"tracking.scan:alpha"}, true)
	srcs, err := oneScanner(t, h, "alpha").Scan(context.Background(), scanTitle, scanEpisodes)
	if err != nil {
		t.Fatal(err)
	}
	if len(srcs) != 2 {
		t.Fatalf("sources %+v", srcs)
	}
	a := srcs[0]
	if a.Kind != "alpha" || a.TitleID != 7 || a.Season != 1 || a.Episode != 1 || a.Ref != "r1" || a.Provider != "p" ||
		a.Quality != "1080p" || !slices.Equal(a.Labels, []string{"x"}) || a.Meta.SourceID != "s1" || a.Meta.Lang != "ru" ||
		a.Meta.ItemID != 0 || a.Meta.FileID != 0 {
		t.Fatalf("source %+v", a)
	}
	if srcs[1].Kind != "alpha" {
		t.Fatalf("second source kind %q", srcs[1].Kind)
	}
	cs := calls(t, tg, "fake")
	if len(cs) != 1 || cs[0].Name != "tracking.scan:alpha" || cs[0].User != "" {
		t.Fatalf("calls %+v", cs)
	}
	var body struct {
		Title struct {
			ID     int64  `json:"id"`
			TMDBID int64  `json:"tmdbId"`
			Name   string `json:"name"`
		} `json:"title"`
		Episodes []struct {
			Season, Number int
			Name           string
		} `json:"episodes"`
	}
	if err := json.Unmarshal(cs[0].Body, &body); err != nil || body.Title.ID != 7 || body.Title.TMDBID != 42 ||
		body.Title.Name != "Тайтл" || len(body.Episodes) != 1 || body.Episodes[0].Name != "Первая" {
		t.Fatalf("body %s %v", cs[0].Body, err)
	}
}

func TestScanner_Unchanged(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "fake", `{"tracking.scan:alpha": {"body": {"unchanged": true, "sources": [{"ref": "x"}]}}}`,
		[]string{"tracking.scan:alpha"}, true)
	_, err := oneScanner(t, h, "alpha").Scan(context.Background(), scanTitle, scanEpisodes)
	if !errors.Is(err, tracking.ErrUnchanged) {
		t.Fatalf("err = %v", err)
	}
}

func TestScanner_Down(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "fake", `{"tracking.scan:alpha": {"status": 500}, "tracking.scan:beta": {"raw": "not json"}}`,
		[]string{"tracking.scan:alpha", "tracking.scan:beta"}, true)
	scs := h.Scanners(context.Background())
	if !slices.Equal(scannerKinds(scs), []string{"alpha", "beta"}) {
		t.Fatalf("scanners %v", scannerKinds(scs))
	}
	for _, sc := range scs {
		if _, err := sc.Scan(context.Background(), scanTitle, scanEpisodes); err == nil || errors.Is(err, tracking.ErrUnchanged) {
			t.Fatalf("%s: err = %v", sc.Kind(), err)
		}
	}
	// stopped between the list and the call: down
	if err := tg.sup.Stop("fake"); err != nil {
		t.Fatal(err)
	}
	if _, err := scs[0].Scan(context.Background(), scanTitle, scanEpisodes); !errors.Is(err, ErrHookDown) {
		t.Fatalf("stopped: err = %v", err)
	}
	// a stopped addon has no scanner at all
	if scs := h.Scanners(context.Background()); len(scs) != 0 {
		t.Fatalf("stopped addon scanners %v", scannerKinds(scs))
	}
	// timeout: an error too
	h.timeouts = map[string]time.Duration{"tracking.scan:alpha": 200 * time.Millisecond}
	hookAddon(t, tg, "slow", `{"tracking.scan:alpha": {"delayMs": 5000, "body": {"sources": []}}}`, []string{"tracking.scan:alpha"}, true)
	if _, err := oneScanner(t, h, "alpha").Scan(context.Background(), scanTitle, scanEpisodes); !errors.Is(err, ErrHookTimeout) {
		t.Fatalf("slow: err = %v", err)
	}
}

// scanEnv is a tracking service over a real store, the title scanTitle
// tracked with an episode list of one, and logs of both sides.
type scanEnv struct {
	svc   *tracking.Service
	title tracking.Title
	logs  *bytes.Buffer
}

type fixedSkeleton struct{}

func (fixedSkeleton) Episodes(context.Context, tracking.Title) ([]tracking.Episode, string, error) {
	return []tracking.Episode{{Season: 1, Number: 1}, {Season: 1, Number: 2}}, "airing", nil
}

// coreScanner is an in-core scanner (as the online one) with fixed rows.
type coreScanner struct {
	kind string
	srcs []tracking.Source
}

func (c *coreScanner) Kind() string { return c.kind }
func (c *coreScanner) Scan(context.Context, tracking.Title, []tracking.Episode) ([]tracking.Source, error) {
	return c.srcs, nil
}

func newScanEnv(t *testing.T, h *Hooks, core ...tracking.Scanner) *scanEnv {
	t.Helper()
	var logs bytes.Buffer
	lg := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h.Log = lg
	st := &tracking.Store{DB: openTestDB(t)}
	ti, err := st.EnsureTitle(context.Background(), "series", 42, "Тайтл", "")
	if err != nil {
		t.Fatal(err)
	}
	svc := &tracking.Service{Store: st, Skeleton: fixedSkeleton{}, Scanners: core, Log: lg}
	svc.SetScanners(h.Scanners)
	return &scanEnv{svc: svc, title: ti, logs: &logs}
}

func (e *scanEnv) refresh(t *testing.T) {
	t.Helper()
	if err := e.svc.Refresh(context.Background(), e.title.ID); err != nil {
		t.Fatal(err)
	}
}

// rows: kind:ref@lastSeen of every stored source.
func (e *scanEnv) rows(t *testing.T) string {
	t.Helper()
	src, err := e.svc.Store.Sources(context.Background(), e.title.ID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range src {
		out = append(out, s.Kind+":"+s.Ref+"@"+s.LastSeen.Format(time.RFC3339))
	}
	return strings.Join(out, ",")
}

// TestScanner_NoContent (E24): a 204 and a 404 answer leave the kind's
// stored rows as they are (none retired, none added, last seen kept) and
// are no failure.
func TestScanner_NoContent(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "fake", `{"tracking.scan:alpha": {"status": 204}}`, []string{"tracking.scan:alpha", "tracking.scan:beta"}, true)
	e := newScanEnv(t, h)
	old := time.Now().Add(-48 * time.Hour)
	for _, k := range []string{"alpha", "beta"} {
		if _, err := e.svc.Store.UpsertSources(context.Background(), e.title.ID, k,
			[]tracking.Source{{Season: 1, Episode: 1, Ref: k + "-1"}}, old); err != nil {
			t.Fatal(err)
		}
	}
	before := e.rows(t)
	e.refresh(t) // alpha: 204; beta: not scripted → 404
	if got := e.rows(t); got != before {
		t.Fatalf("rows\n%s\nwant\n%s", got, before)
	}
	if n := len(calls(t, tg, "fake")); n != 2 {
		t.Fatalf("hook calls %d", n)
	}
	if strings.Contains(e.logs.String(), "level=WARN") {
		t.Fatalf("logged as a failure:\n%s", e.logs)
	}
}

// TestRefresh_MixedKinds: an in-core scanner and an addon's kind side by
// side; each updates only its own rows, and an addon that is stopped
// (quietly) or fails (logged) leaves its rows untouched while the in-core
// one goes on.
func TestRefresh_MixedKinds(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "fake", `{"tracking.scan:alpha": {"body": {"sources": [{"season": 1, "episode": 1, "ref": "a1"}]}}}`,
		[]string{"tracking.scan:alpha"}, true)
	onl := &coreScanner{kind: "online", srcs: []tracking.Source{{Season: 1, Episode: 1, Ref: "o1"}}}
	e := newScanEnv(t, h, onl)
	kinds := func() string {
		src, _ := e.svc.Store.Sources(context.Background(), e.title.ID)
		var out []string
		for _, s := range src {
			out = append(out, s.Kind+":"+s.Ref)
		}
		return strings.Join(out, ",")
	}
	e.refresh(t)
	if got := kinds(); got != "alpha:a1,online:o1" {
		t.Fatalf("round 1: %s", got)
	}
	// stopped: no scanner this round, nothing logged, alpha kept
	if err := tg.sup.Stop("fake"); err != nil {
		t.Fatal(err)
	}
	onl.srcs = []tracking.Source{{Season: 1, Episode: 2, Ref: "o2"}}
	e.refresh(t)
	if got := kinds(); got != "alpha:a1,online:o2" {
		t.Fatalf("round 2: %s", got)
	}
	if strings.Contains(e.logs.String(), "level=WARN") {
		t.Fatalf("stopped addon logged:\n%s", e.logs)
	}
	// running but failing: logged, alpha kept, online goes on
	hookAddon(t, tg, "fake", `{"tracking.scan:alpha": {"status": 500}}`, []string{"tracking.scan:alpha"}, true)
	onl.srcs = []tracking.Source{{Season: 1, Episode: 1, Ref: "o3"}}
	e.refresh(t)
	if got := kinds(); got != "alpha:a1,online:o3" {
		t.Fatalf("round 3: %s", got)
	}
	if !strings.Contains(e.logs.String(), "level=WARN") || !strings.Contains(e.logs.String(), "kind=alpha") {
		t.Fatalf("failure not logged:\n%s", e.logs)
	}
}

// TestScanners_EnabledRunningOnly: a disabled addon gives no scanner (its
// stored rows stay, nothing is logged each round); a kind declared twice
// (which install refuses) is the first addon's; library is never an addon's,
// online is (D12).
func TestScanners_EnabledRunningOnly(t *testing.T) {
	h, tg := newHooks(t)
	var logs bytes.Buffer
	h.Log = slog.New(slog.NewTextHandler(&logs, nil))
	hookAddon(t, tg, "a", `{}`, []string{"tracking.scan:alpha", "tracking.scan:library", "tracking.scan:online"}, true)
	hookAddon(t, tg, "b", `{}`, []string{"tracking.scan:alpha", "tracking.scan:beta"}, true)
	hookAddon(t, tg, "c", `{}`, []string{"tracking.scan:zeta"}, true)
	off := tg.ins["c"]
	off.Enabled = false
	tg.ins["c"] = off
	scs := h.Scanners(context.Background())
	if got := scannerKinds(scs); !slices.Equal(got, []string{"alpha", "online", "beta"}) {
		t.Fatalf("kinds %v", got)
	}
	if scs[0].(*hookScanner).addon != "a" {
		t.Fatalf("alpha from %q", scs[0].(*hookScanner).addon)
	}
	if logs.Len() != 0 {
		t.Fatalf("logged: %s", logs.String())
	}
}

// TestHooks_ScanKinds (E2): the kinds a profile may see of the installed
// addons it has access to, enabled or not (online too, D12); kids none
// (they keep the library's).
func TestHooks_ScanKinds(t *testing.T) {
	tg := &hookTarget{ins: map[string]Installed{
		"a": {ID: "a", Enabled: true, Manifest: Manifest{ID: "a", Access: "adults", Hooks: []string{"tracking.scan:beta", "tracking.scan:alpha", "disk.usage"}}},
		"b": {ID: "b", Enabled: false, Manifest: Manifest{ID: "b", Access: "adults", Hooks: []string{"tracking.scan:zeta"}}},
		"c": {ID: "c", Enabled: true, Manifest: Manifest{ID: "c", Hooks: []string{"tracking.scan:gamma"}}},
		"d": {ID: "d", Enabled: true, Manifest: Manifest{ID: "d", Access: "adults", Hooks: []string{"tracking.scan:online", "tracking.scan:library"}}},
	}}
	h := &Hooks{T: tg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if got := h.ScanKinds(auth.User{ID: 1, IsAdmin: true}); !slices.Equal(got, []string{"alpha", "beta", "gamma", "online", "zeta"}) {
		t.Fatalf("admin %v", got)
	}
	if got := h.ScanKinds(auth.User{ID: 2}); !slices.Equal(got, []string{"alpha", "beta", "online", "zeta"}) {
		t.Fatalf("adult %v", got)
	}
	if got := h.ScanKinds(auth.User{ID: 3, IsKid: true}); len(got) != 0 {
		t.Fatalf("kid %v", got)
	}
	var none *Hooks
	if got := none.ScanKinds(auth.User{ID: 1, IsAdmin: true}); len(got) != 0 {
		t.Fatalf("nil hooks %v", got)
	}
}

// ensured records the titles ExtraTracked's ensure was given; each gets
// the id 100 + its TMDB id.
type ensured struct{ got []string }

func (e *ensured) ensure(_ context.Context, kind string, tmdbID int64, name, poster string) (int64, error) {
	e.got = append(e.got, kind+":"+strconv.FormatInt(tmdbID, 10)+":"+name+":"+poster)
	return 100 + tmdbID, nil
}

// TestExtraTracked_EnsuresTitles (E5): every addon's tracking.titles are
// made tracked titles (ensure) and their ids are the extra tracked ones,
// once each; a title of another kind or without a TMDB id is left out.
func TestExtraTracked_EnsuresTitles(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "a", `{"tracking.titles": {"body": {"titles": [
		{"kind": "series", "tmdbId": 42, "name": "Сериал", "poster": "/p.jpg"},
		{"kind": "movie", "tmdbId": 603},
		{"kind": "music", "tmdbId": 5},
		{"kind": "series", "tmdbId": 0}]}}}`, []string{"tracking.titles"}, true)
	hookAddon(t, tg, "b", `{"tracking.titles": {"body": {"titles": [{"kind": "series", "tmdbId": 42, "name": "Другое"}]}}}`,
		[]string{"tracking.titles"}, true)
	var e ensured
	ids, err := h.ExtraTracked(e.ensure)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []int64{142, 703}) {
		t.Fatalf("ids %v", ids)
	}
	if want := []string{"series:42:Сериал:/p.jpg", "movie:603::"}; !slices.Equal(e.got, want) {
		t.Fatalf("ensured %v, want %v", e.got, want)
	}
	if cs := calls(t, tg, "a"); len(cs) != 1 || cs[0].User != "" {
		t.Fatalf("calls %+v", cs)
	}
}

// TestExtraTracked_DownThenBack (E5): an addon that is down adds no titles
// this round, and no error (the round goes on); the next round has them.
func TestExtraTracked_DownThenBack(t *testing.T) {
	h, tg := newHooks(t)
	script := `{"tracking.titles": {"body": {"titles": [{"kind": "series", "tmdbId": 42}]}}}`
	hookAddon(t, tg, "a", script, []string{"tracking.titles"}, false)
	var e ensured
	extra := h.ExtraTracked(e.ensure)
	ids, err := extra(context.Background())
	if err != nil || len(ids) != 0 {
		t.Fatalf("down: %v %v", ids, err)
	}
	if err := tg.sup.Start(context.Background(), tg.ins["a"]); err != nil {
		t.Fatal(err)
	}
	if ids, err = extra(context.Background()); err != nil || !slices.Equal(ids, []int64{142}) {
		t.Fatalf("back: %v %v", ids, err)
	}
	// an ensure that fails leaves that title out, not the round
	failing := func(context.Context, string, int64, string, string) (int64, error) { return 0, errors.New("db") }
	if ids, err = h.ExtraTracked(failing)(context.Background()); err != nil || len(ids) != 0 {
		t.Fatalf("failing ensure: %v %v", ids, err)
	}
}

// TestWatched_Merge (E6): the addons' tracking.watched answers merged, a
// profile's episode watched when any says so, at the furthest position; a
// down or failing addon is left out.
func TestWatched_Merge(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "a", `{"tracking.watched": {"body": {"episodes": [
		{"season": 1, "number": 1, "positionSec": 30},
		{"season": 1, "number": 2, "watched": true, "positionSec": 5}]}}}`, []string{"tracking.watched"}, true)
	hookAddon(t, tg, "b", `{"tracking.watched": {"body": {"episodes": [
		{"season": 1, "number": 1, "watched": true, "positionSec": 90},
		{"season": 1, "number": 3, "positionSec": 50}]}}}`, []string{"tracking.watched"}, true)
	hookAddon(t, tg, "c", `{"tracking.watched": {"status": 500}}`, []string{"tracking.watched"}, true)
	hookAddon(t, tg, "d", `{}`, []string{"tracking.watched"}, false)
	ctx := context.Background()
	w, err := h.Watched()(ctx, 3, scanTitle)
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != 2 || !w[[2]int{1, 1}] || !w[[2]int{1, 2}] {
		t.Fatalf("watched %v", w)
	}
	st, err := h.WatchStates()(ctx, 3, "series", 42)
	if err != nil {
		t.Fatal(err)
	}
	want := map[[2]int]tracking.WatchState{{1, 1}: {Watched: true, PositionSec: 90}, {1, 2}: {Watched: true, PositionSec: 5},
		{1, 3}: {PositionSec: 50}}
	if !maps.Equal(st, want) {
		t.Fatalf("states %v, want %v", st, want)
	}
	var body struct {
		UserID int64  `json:"userId"`
		Kind   string `json:"kind"`
		TMDBID int64  `json:"tmdbId"`
	}
	cs := calls(t, tg, "a")
	if len(cs) != 2 || json.Unmarshal(cs[0].Body, &body) != nil || body.UserID != 3 || body.Kind != "series" || body.TMDBID != 42 {
		t.Fatalf("calls %+v", cs)
	}
}

// TestInterest_Dedup (E7): the profiles every addon names for a title,
// merged once each, in id order; no positive id, no profile.
func TestInterest_Dedup(t *testing.T) {
	h, tg := newHooks(t)
	hookAddon(t, tg, "a", `{"notify.interest": {"body": {"userIds": [3, 1, 2, 0, -4]}}}`, []string{"notify.interest"}, true)
	hookAddon(t, tg, "b", `{"notify.interest": {"body": {"userIds": [2, 5]}}}`, []string{"notify.interest"}, true)
	ids, err := h.Interest()(context.Background(), "series", 42)
	if err != nil || !slices.Equal(ids, []int64{1, 2, 3, 5}) {
		t.Fatalf("ids %v %v", ids, err)
	}
	var body struct {
		Kind   string `json:"kind"`
		TMDBID int64  `json:"tmdbId"`
	}
	cs := calls(t, tg, "a")
	if len(cs) != 1 || cs[0].User != "" || json.Unmarshal(cs[0].Body, &body) != nil || body.Kind != "series" || body.TMDBID != 42 {
		t.Fatalf("calls %+v", cs)
	}
}

// TestInterest_Down (E7): an addon that is down, fails or is slow (left
// out within the hook's 5 s) never fails the letters: the others' ids
// stand.
func TestInterest_Down(t *testing.T) {
	h, tg := newHooks(t)
	h.timeouts = map[string]time.Duration{"notify.interest": 300 * time.Millisecond}
	hookAddon(t, tg, "a", `{"notify.interest": {"body": {"userIds": [7]}}}`, []string{"notify.interest"}, true)
	hookAddon(t, tg, "b", `{"notify.interest": {"status": 500}}`, []string{"notify.interest"}, true)
	hookAddon(t, tg, "c", `{"notify.interest": {"delayMs": 5000, "body": {"userIds": [8]}}}`, []string{"notify.interest"}, true)
	hookAddon(t, tg, "d", `{}`, []string{"notify.interest"}, false)
	t0 := time.Now()
	ids, err := h.Interest()(context.Background(), "movie", 603)
	if err != nil || !slices.Equal(ids, []int64{7}) {
		t.Fatalf("ids %v %v", ids, err)
	}
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("took %v", d)
	}
	if HookTimeout("notify.interest") != 5*time.Second {
		t.Fatalf("timeout %v", HookTimeout("notify.interest"))
	}
}
