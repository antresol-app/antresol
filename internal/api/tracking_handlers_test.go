package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/tracking"
)

// trackSkeleton is a fake TMDB skeleton: fixed episodes and info.
type trackSkeleton struct {
	mu    sync.Mutex
	eps   []tracking.Episode
	info  tracking.TitleInfo
	calls int
	delay time.Duration // each fetch takes this long (overlapping requests)
}

func (f *trackSkeleton) Episodes(ctx context.Context, t tracking.Title) ([]tracking.Episode, string, error) {
	eps, st, _, err := f.EpisodesInfo(ctx, t)
	return eps, st, err
}

func (f *trackSkeleton) EpisodesInfo(_ context.Context, t tracking.Title) ([]tracking.Episode, string, tracking.TitleInfo, error) {
	time.Sleep(f.delay)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.eps, "airing", f.info, nil
}

type trackEnv struct {
	*libEnv
	svc *tracking.Service
	sk  *trackSkeleton
}

func newTrackEnv(t *testing.T) *trackEnv { return newTrackEnvWith(t, nil) }

// newTrackEnvWith is newTrackEnv with more of the router's deps set.
func newTrackEnvWith(t *testing.T, opt func(*Deps)) *trackEnv {
	today := time.Now().Format(time.DateOnly)
	sk := &trackSkeleton{eps: []tracking.Episode{{Season: 1, Number: 1, Name: "Первая", AirDate: today},
		{Season: 1, Number: 2, Name: "Вторая", AirDate: today}}}
	te := &trackEnv{sk: sk}
	te.libEnv = newLibEnvWith(t, func(d *Deps, db *sql.DB) {
		st := &tracking.Store{DB: db}
		te.svc = &tracking.Service{Store: st, Skeleton: sk, Scanners: []tracking.Scanner{tracking.NewLibraryScanner(d.Library)}}
		te.svc.Watched = []tracking.WatchedFunc{tracking.LibraryWatched(d.Library, d.Progress)}
		te.svc.WatchStates = []tracking.WatchStateFunc{tracking.LibraryWatchState(d.Library, d.Progress)}
		d.Tracking = te.svc
		if opt != nil {
			opt(d)
		}
	})
	return te
}

// series adds a library series of two episodes; returns the episode ids.
func (e *trackEnv) series(title, rating string, tmdbID int64) []int64 {
	var ids []int64
	for i := 1; i <= 2; i++ {
		p := filepath.Join(e.media, title+"-s01e0"+itoa(int64(i))+".mkv")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			e.t.Fatal(err)
		}
		id, err := e.repo.UpsertEpisode(context.Background(), library.EpisodeInput{SeriesTitle: title, SeriesRating: rating,
			SeriesTmdbID: tmdbID, Season: 1, Episode: i, File: library.FileInput{Path: p, Size: 1}})
		if err != nil {
			e.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// track marks a title watching for a profile and refreshes it.
func (e *trackEnv) track(userID int64, kind string, tmdbID int64, name string) tracking.Title {
	ctx := context.Background()
	ti, err := e.svc.Store.EnsureTitle(ctx, kind, tmdbID, name, "")
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.svc.Store.SetMark(ctx, tracking.Mark{UserID: userID, TitleID: ti.ID, State: tracking.Watching}); err != nil {
		e.t.Fatal(err)
	}
	return ti
}

func (e *trackEnv) refresh(ti tracking.Title) {
	if err := e.svc.Refresh(context.Background(), ti.ID); err != nil {
		e.t.Fatal(err)
	}
}

// offLibrary gives a non-library title a source (a torrent) now and its cert.
func (e *trackEnv) offLibrary(ti tracking.Title, cert string) {
	ctx := context.Background()
	if _, err := e.svc.Store.UpsertSources(ctx, ti.ID, "torrent", []tracking.Source{{Season: 1, Episode: 1, Ref: "t"}}, time.Now()); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE tracked_titles SET cert=? WHERE id=?`, cert, ti.ID); err != nil {
		e.t.Fatal(err)
	}
}

type feedOut struct {
	Items []struct {
		Title struct {
			Kind   string `json:"kind"`
			TmdbID int64  `json:"tmdbId"`
			Name   string `json:"name"`
			ItemID int64  `json:"itemId"`
		} `json:"title"`
		Season    int       `json:"season"`
		Episode   int       `json:"episode"`
		Name      string    `json:"name"`
		FirstSeen time.Time `json:"firstSeen"`
		Kinds     []string  `json:"kinds"`
	} `json:"items"`
}

func (e *trackEnv) feed(c *http.Cookie) feedOut {
	w := e.do("GET", "/api/tracking/feed?days=14", "", c)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	return decodeJSON[feedOut](e.t, w.Body.Bytes())
}

func TestMarkRoundTrip(t *testing.T) {
	e := newTrackEnv(t)
	w := e.do("PUT", "/api/marks/series/42", `{"state":"planned","score":null,"name":"Шоу","poster":"/p.jpg"}`, e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	type markOut struct {
		Mark *struct {
			State string `json:"state"`
			Score *int   `json:"score"`
		} `json:"mark"`
	}
	if m := decodeJSON[markOut](t, w.Body.Bytes()); m.Mark == nil || m.Mark.State != "planned" || m.Mark.Score != nil {
		t.Fatalf("%s", w.Body)
	}
	w = e.do("PUT", "/api/marks/series/42", `{"state":"watching","score":8}`, e.admin)
	if m := decodeJSON[markOut](t, w.Body.Bytes()); w.Code != 200 || m.Mark.State != "watching" || *m.Mark.Score != 8 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/marks/series/42", "", e.admin)
	if m := decodeJSON[markOut](t, w.Body.Bytes()); w.Code != 200 || m.Mark == nil || m.Mark.State != "watching" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/api/marks?state=planned,watching", "", e.admin)
	var list struct {
		Items []struct {
			Title struct {
				Name   string `json:"name"`
				TmdbID int64  `json:"tmdbId"`
				Kind   string `json:"kind"`
			} `json:"title"`
			Mark struct {
				State string `json:"state"`
			} `json:"mark"`
		} `json:"items"`
	}
	json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Items) != 1 || list.Items[0].Title.Name != "Шоу" || list.Items[0].Title.TmdbID != 42 ||
		list.Items[0].Title.Kind != "series" || list.Items[0].Mark.State != "watching" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/marks?state=completed", "", e.admin); w.Code != 200 || len(decodeJSON[struct{ Items []any }](t, w.Body.Bytes()).Items) != 0 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, bad := range []struct{ path, body string }{
		{"/api/marks/series/42", `{"state":"loved"}`},
		{"/api/marks/series/42", `{"state":"planned","score":11}`},
		{"/api/marks/album/42", `{"state":"planned"}`},
		{"/api/marks/series/x", `{"state":"planned"}`},
	} {
		if w := e.do("PUT", bad.path, bad.body, e.admin); w.Code != 400 {
			t.Fatalf("%s %s: %d", bad.path, bad.body, w.Code)
		}
	}
	if w := e.do("GET", "/api/marks?state=nope", "", e.admin); w.Code != 400 {
		t.Fatalf("bad state filter %d", w.Code)
	}
	if w := e.do("DELETE", "/api/marks/series/42", "", e.admin); w.Code != 204 {
		t.Fatalf("delete %d", w.Code)
	}
	w = e.do("GET", "/api/marks/series/42", "", e.admin)
	if w.Code != 200 || decodeJSON[markOut](t, w.Body.Bytes()).Mark != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("DELETE", "/api/marks/movie/9", "", e.admin); w.Code != 204 {
		t.Fatalf("delete absent %d", w.Code)
	}
}

func TestFeedExcludesWatched(t *testing.T) {
	e := newTrackEnv(t)
	eps := e.series("Сёгун", "16+", 42)
	e.refresh(e.track(1, "series", 42, "Сёгун"))
	f := e.feed(e.admin)
	if len(f.Items) != 2 || f.Items[0].Title.ItemID == 0 || f.Items[0].Title.Name != "Сёгун" || f.Items[0].Kinds[0] != "library" {
		t.Fatalf("%+v", f)
	}
	if w := e.do("POST", "/api/items/"+itoa(eps[0])+"/watched", `{"watched":true}`, e.admin); w.Code/100 != 2 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	f = e.feed(e.admin)
	if len(f.Items) != 1 || f.Items[0].Episode != 2 || f.Items[0].Name != "Вторая" {
		t.Fatalf("%+v", f)
	}
}

func TestFeedHonoursKidAndRating(t *testing.T) {
	e := newTrackEnv(t)
	kid := e.addUser(`{"name":"Вася","isKid":true,"maxRating":"12+"}`, e.admin)
	lim := e.addUser(`{"name":"Гость","maxRating":"16+"}`, e.admin)
	e.series("Сёгун", "18+", 42)
	e.series("Мульт", "0+", 43)
	for _, uid := range []int64{1, kid.ID, lim.ID} {
		e.refresh(e.track(uid, "series", 42, "Сёгун"))
		e.refresh(e.track(uid, "series", 43, "Мульт"))
		e.offLibrary(e.track(uid, "series", 70, "Чужое детское"), "0+")
		e.offLibrary(e.track(uid, "series", 71, "Чужое взрослое"), "18+")
		e.offLibrary(e.track(uid, "series", 72, "Чужое без рейтинга"), "")
	}
	names := func(c *http.Cookie) map[string]bool {
		out := map[string]bool{}
		for _, it := range e.feed(c).Items {
			out[it.Title.Name] = true
		}
		return out
	}
	cookie := func(id int64) *http.Cookie {
		return session(e.do("POST", "/api/login", `{"userId":`+itoa(id)+`}`, nil))
	}
	if got := names(e.admin); len(got) != 5 {
		t.Fatalf("admin %v", got)
	}
	// a kid: library titles within its rating only
	if got := names(cookie(kid.ID)); len(got) != 1 || !got["Мульт"] {
		t.Fatalf("kid %v", got)
	}
	// a limited adult: also titles off the library with a stored rating within the limit
	if got := names(cookie(lim.ID)); len(got) != 2 || !got["Мульт"] || !got["Чужое детское"] {
		t.Fatalf("limited %v", got)
	}
	// the calendar keeps the same gate
	w := e.do("GET", "/api/tracking/calendar?from="+time.Now().AddDate(0, 0, -1).Format(time.DateOnly)+"&to="+time.Now().AddDate(0, 0, 1).Format(time.DateOnly), "", cookie(kid.ID))
	var cal struct {
		Days []struct {
			Date  string `json:"date"`
			Items []struct {
				Title struct {
					Name string `json:"name"`
				} `json:"title"`
			} `json:"items"`
		} `json:"days"`
	}
	json.Unmarshal(w.Body.Bytes(), &cal)
	if w.Code != 200 || len(cal.Days) != 1 || len(cal.Days[0].Items) != 2 || cal.Days[0].Items[0].Title.Name != "Мульт" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// a kid cannot mark or open a title it may not see
	kc := cookie(kid.ID)
	if w := e.do("PUT", "/api/marks/series/42", `{"state":"planned"}`, kc); w.Code != 404 {
		t.Fatalf("kid mark %d", w.Code)
	}
	if w := e.do("GET", "/api/tracking/series/71/episodes", "", kc); w.Code != 404 {
		t.Fatalf("kid episodes %d", w.Code)
	}
}

func TestCalendarRange(t *testing.T) {
	e := newTrackEnv(t)
	for _, c := range []struct {
		q    string
		code int
	}{
		{"from=2026-01-01&to=2026-02-12", 200}, // 42 days
		{"from=2026-01-01&to=2026-02-13", 400},
		{"from=2026-02-01&to=2026-01-01", 400},
		{"from=2026-01-01", 400},
		{"from=2026-13-01&to=2026-12-01", 400},
	} {
		w := e.do("GET", "/api/tracking/calendar?"+c.q, "", e.admin)
		if w.Code != c.code {
			t.Fatalf("%s: %d %s", c.q, w.Code, w.Body)
		}
		if c.code == 200 && w.Body.String() != "{\"days\":[]}\n" {
			t.Fatalf("%s", w.Body)
		}
	}
}

func TestHomeHasTrackedShelfOnlyWhenNonEmpty(t *testing.T) {
	e := newTrackEnv(t)
	type homeOut struct {
		Shelves []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Cards []Card `json:"cards"`
		} `json:"shelves"`
	}
	home := func() homeOut {
		w := e.do("GET", "/api/home", "", e.admin)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		return decodeJSON[homeOut](t, w.Body.Bytes())
	}
	for _, s := range home().Shelves {
		if s.ID == "tracked-new" {
			t.Fatal("empty shelf shown")
		}
	}
	e.series("Сёгун", "16+", 42)
	e.refresh(e.track(1, "series", 42, "Сёгун"))
	e.offLibrary(e.track(1, "series", 70, "Чужое"), "")
	h := home()
	if len(h.Shelves) < 2 || h.Shelves[0].ID != "continue" || h.Shelves[1].ID != "tracked-new" || h.Shelves[1].Title != "Вышло новое" {
		t.Fatalf("%+v", h.Shelves)
	}
	cards := h.Shelves[1].Cards
	// one card per title
	if len(cards) != 2 {
		t.Fatalf("%+v", cards)
	}
	var lib, off *Card
	for i := range cards {
		if cards[i].ID != 0 {
			lib = &cards[i]
		} else {
			off = &cards[i]
		}
	}
	if lib == nil || off == nil || lib.Title != "Сёгун" || !lib.OnServer || lib.Subtitle == "" {
		t.Fatalf("%+v", cards)
	}
	if off.Title != "Чужое" || off.TmdbID != 70 || off.TmdbKind != "series" || off.Season != 1 || off.Episode != 1 {
		t.Fatalf("%+v", off)
	}
	w := e.do("GET", "/api/home", "", e.admin, "Accept-Language", "en")
	if h := decodeJSON[homeOut](t, w.Body.Bytes()); h.Shelves[1].Title != "New for you" {
		t.Fatalf("en %+v", h.Shelves[1].Title)
	}
}

func TestEpisodesOnDemandDoesNotMark(t *testing.T) {
	e := newTrackEnv(t)
	e.sk.info = tracking.TitleInfo{Name: "Новинка", Poster: "/n.jpg", Cert: "12+"}
	w := e.do("GET", "/api/tracking/series/55/episodes", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		Title struct {
			Name   string `json:"name"`
			Poster string `json:"poster"`
		} `json:"title"`
		Episodes []struct {
			Season  int               `json:"season"`
			Number  int               `json:"number"`
			Name    string            `json:"name"`
			AirDate string            `json:"airDate"`
			Sources []json.RawMessage `json:"sources"`
		} `json:"episodes"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Title.Name != "Новинка" || out.Title.Poster == "" || len(out.Episodes) != 2 || out.Episodes[1].Name != "Вторая" || out.Episodes[0].Sources == nil {
		t.Fatalf("%s", w.Body)
	}
	var marks, titles int
	e.db.QueryRow(`SELECT count(*) FROM title_marks`).Scan(&marks)
	e.db.QueryRow(`SELECT count(*) FROM tracked_titles`).Scan(&titles)
	if marks != 0 || titles != 1 {
		t.Fatalf("marks=%d titles=%d", marks, titles)
	}
	// fresh: not fetched again
	e.do("GET", "/api/tracking/series/55/episodes", "", e.admin)
	if e.sk.calls != 1 {
		t.Fatalf("calls %d", e.sk.calls)
	}
	// a library title's sources come with it
	e.series("Сёгун", "16+", 42)
	w = e.do("GET", "/api/tracking/series/42/episodes", "", e.admin)
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || len(out.Episodes) != 2 || len(out.Episodes[0].Sources) != 1 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func (e *trackEnv) onWatchlist(userID, itemID int64) bool {
	ids, err := e.prog.Watchlist(context.Background(), userID)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, id := range ids {
		if id == itemID {
			return true
		}
	}
	return false
}

func TestMarkPlannedSyncsWatchlist(t *testing.T) {
	e := newTrackEnv(t)
	e.series("Шоу", "", 77)
	it, err := e.repo.ItemByTMDB(context.Background(), library.KindSeries, 77)
	if err != nil {
		t.Fatal(err)
	}
	uid := int64(1)
	if w := e.do("PUT", "/api/marks/series/77", `{"state":"planned"}`, e.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !e.onWatchlist(uid, it.ID) {
		t.Fatal("planned: not on the watchlist")
	}
	if w := e.do("PUT", "/api/marks/series/77", `{"state":"watching"}`, e.admin); w.Code != 200 || e.onWatchlist(uid, it.ID) {
		t.Fatalf("watching: %d, still on the watchlist", w.Code)
	}
	e.do("PUT", "/api/marks/series/77", `{"state":"planned"}`, e.admin)
	if w := e.do("DELETE", "/api/marks/series/77", "", e.admin); w.Code != 204 || e.onWatchlist(uid, it.ID) {
		t.Fatalf("delete: %d, still on the watchlist", w.Code)
	}
	// a title off the library has no watchlist row to keep
	if w := e.do("PUT", "/api/marks/series/5000", `{"state":"planned"}`, e.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestWatchlistShowsAsPlanned(t *testing.T) {
	e := newTrackEnv(t)
	e.series("Шоу", "", 78)
	it, err := e.repo.ItemByTMDB(context.Background(), library.KindSeries, 78)
	if err != nil {
		t.Fatal(err)
	}
	type markOut struct {
		Mark *struct {
			State string `json:"state"`
		} `json:"mark"`
	}
	if m := decodeJSON[markOut](t, e.do("GET", "/api/marks/series/78", "", e.admin).Body.Bytes()); m.Mark != nil {
		t.Fatalf("no watchlist row, yet %+v", m.Mark)
	}
	if err := e.prog.AddToWatchlist(context.Background(), 1, it.ID); err != nil {
		t.Fatal(err)
	}
	w := e.do("GET", "/api/marks/series/78", "", e.admin)
	if m := decodeJSON[markOut](t, w.Body.Bytes()); w.Code != 200 || m.Mark == nil || m.Mark.State != "planned" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

type scoredMark struct {
	Mark *struct {
		State string `json:"state"`
		Score *int   `json:"score"`
	} `json:"mark"`
}

func (e *trackEnv) markOf(path string) scoredMark {
	w := e.do("GET", path, "", e.admin)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	return decodeJSON[scoredMark](e.t, w.Body.Bytes())
}

func TestWatchlistPostCreatesPlannedMark(t *testing.T) {
	e := newTrackEnv(t)
	e.series("Шоу", "", 80)
	it, _ := e.repo.ItemByTMDB(context.Background(), library.KindSeries, 80)
	id := itoa(it.ID)
	if w := e.do("POST", "/api/watchlist/"+id, "", e.admin); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if m := e.markOf("/api/marks/series/80"); m.Mark == nil || m.Mark.State != "planned" {
		t.Fatalf("no mark: %+v", m.Mark)
	}
	// an existing mark in another state becomes planned, its score stays
	e.do("PUT", "/api/marks/series/80", `{"state":"dropped","score":4}`, e.admin)
	e.do("DELETE", "/api/watchlist/"+id, "", e.admin)
	e.do("PUT", "/api/marks/series/80", `{"state":"dropped"}`, e.admin)
	if w := e.do("POST", "/api/watchlist/"+id, "", e.admin); w.Code != 204 {
		t.Fatalf("%d", w.Code)
	}
	if m := e.markOf("/api/marks/series/80"); m.Mark == nil || m.Mark.State != "planned" || m.Mark.Score == nil || *m.Mark.Score != 4 {
		t.Fatalf("%+v", m.Mark)
	}
	if !e.onWatchlist(1, it.ID) {
		t.Fatal("row gone")
	}
}

func TestWatchlistDeleteRemovesPlannedMarkOnly(t *testing.T) {
	e := newTrackEnv(t)
	e.series("Шоу", "", 81)
	it, _ := e.repo.ItemByTMDB(context.Background(), library.KindSeries, 81)
	id := itoa(it.ID)
	e.do("POST", "/api/watchlist/"+id, "", e.admin)
	if w := e.do("DELETE", "/api/watchlist/"+id, "", e.admin); w.Code != 204 {
		t.Fatalf("%d", w.Code)
	}
	if m := e.markOf("/api/marks/series/81"); m.Mark != nil {
		t.Fatalf("planned mark kept: %+v", m.Mark)
	}
	// a mark in another state is left alone
	e.do("PUT", "/api/marks/series/81", `{"state":"watching"}`, e.admin)
	e.do("POST", "/api/watchlist/"+id, "", e.admin) // makes it planned again…
	e.do("PUT", "/api/marks/series/81", `{"state":"watching"}`, e.admin)
	e.do("DELETE", "/api/watchlist/"+id, "", e.admin)
	if m := e.markOf("/api/marks/series/81"); m.Mark == nil || m.Mark.State != "watching" {
		t.Fatalf("%+v", m.Mark)
	}
}

func TestPutMarkKeepsScore(t *testing.T) {
	e := newTrackEnv(t)
	e.do("PUT", "/api/marks/series/90", `{"state":"watching","score":8}`, e.admin)
	e.do("PUT", "/api/marks/series/90", `{"state":"completed"}`, e.admin)
	if m := e.markOf("/api/marks/series/90"); m.Mark == nil || m.Mark.State != "completed" || m.Mark.Score == nil || *m.Mark.Score != 8 {
		t.Fatalf("score lost: %+v", m.Mark)
	}
	e.do("PUT", "/api/marks/series/90", `{"state":"completed","score":null}`, e.admin)
	if m := e.markOf("/api/marks/series/90"); m.Mark == nil || m.Mark.Score != nil {
		t.Fatalf("null did not clear: %+v", m.Mark)
	}
}

func TestChoiceRoute(t *testing.T) {
	e := newTrackEnv(t)
	kid := e.addUser(`{"name":"Вася","isKid":true,"maxRating":"12+"}`, e.admin)
	kc := session(e.do("POST", "/api/login", `{"userId":`+itoa(kid.ID)+`}`, nil))
	lim := e.addUser(`{"name":"Гость","maxRating":"16+"}`, e.admin)
	lc := session(e.do("POST", "/api/login", `{"userId":`+itoa(lim.ID)+`}`, nil))
	e.offLibrary(e.track(1, "series", 71, "Чужое взрослое"), "18+")
	e.offLibrary(e.track(1, "series", 70, "Чужое детское"), "0+")
	if w := e.do("GET", "/api/tracking/series/70/choice", "", e.admin); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"choice":null}` {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/tracking/series/70/choice", `{"kind":"online","provider":"b1","voice":"A","lang":"ru","quality":"1080p"}`, e.admin); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w := e.do("GET", "/api/tracking/series/70/choice", "", e.admin)
	var out struct {
		Choice *tracking.Choice `json:"choice"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if out.Choice == nil || *out.Choice != (tracking.Choice{Kind: "online", Provider: "b1", Voice: "A", Lang: "ru", Quality: "1080p"}) {
		t.Fatalf("%s", w.Body)
	}
	// per profile
	if w := e.do("GET", "/api/tracking/series/70/choice", "", lc); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"choice":null}` {
		t.Fatalf("other profile %d %s", w.Code, w.Body)
	}
	if w := e.do("PUT", "/api/tracking/series/70/choice", `{"kind":"magnet"}`, e.admin); w.Code != 400 {
		t.Fatalf("bad kind %d", w.Code)
	}
	if w := e.do("PUT", "/api/tracking/series/70/choice", `{}`, e.admin); w.Code != 204 {
		t.Fatalf("empty %d", w.Code)
	}
	// the gate hides a title from a kid
	if w := e.do("GET", "/api/tracking/series/71/choice", "", kc); w.Code != 404 {
		t.Fatalf("kid get %d", w.Code)
	}
	if w := e.do("PUT", "/api/tracking/series/71/choice", `{"kind":"online"}`, kc); w.Code != 404 {
		t.Fatalf("kid put %d", w.Code)
	}
}

// episodesOut is the episodes endpoint's answer, as the apps read it.
type episodesOut struct {
	Episodes []struct {
		Season      int    `json:"season"`
		Number      int    `json:"number"`
		Name        string `json:"name"`
		AirDate     string `json:"airDate"`
		Watched     bool   `json:"watched"`
		PositionSec int    `json:"positionSec"`
		New         bool   `json:"new"`
		Sources     []struct {
			Kind    string              `json:"kind"`
			Ref     string              `json:"ref"`
			Quality string              `json:"quality"`
			Meta    tracking.SourceMeta `json:"meta"`
		} `json:"sources"`
	} `json:"episodes"`
}

func (e *trackEnv) episodes(tmdbID int64, c *http.Cookie) episodesOut {
	e.t.Helper()
	w := e.do("GET", "/api/tracking/series/"+itoa(tmdbID)+"/episodes", "", c)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	return decodeJSON[episodesOut](e.t, w.Body.Bytes())
}

func (o episodesOut) kinds(ep int) []string {
	var k []string
	for _, s := range o.Episodes[ep].Sources {
		k = append(k, s.Kind)
	}
	return k
}

// srcRows gives episode 1 of a title one row of each kind.
func (e *trackEnv) srcRows(ti tracking.Title) {
	ctx := context.Background()
	now := time.Now()
	for _, k := range []string{"pinned", "torrent", "online"} {
		if _, err := e.svc.Store.UpsertSources(ctx, ti.ID, k, []tracking.Source{{Season: 1, Episode: 1, Ref: k + "-ref"}}, now); err != nil {
			e.t.Fatal(err)
		}
	}
}

func TestLibraryRowsCarryEpisodeItem(t *testing.T) {
	e := newTrackEnv(t)
	ids := e.series("Сёгун", "16+", 42)
	fs, err := e.repo.FilesOf(context.Background(), ids[0])
	if err != nil || len(fs) != 1 {
		t.Fatal(err, fs)
	}
	if err := e.repo.SetProbe(context.Background(), fs[0].ID, `{"Video":{"Width":1920,"Height":1080}}`, 1000); err != nil {
		t.Fatal(err)
	}
	e.refresh(e.track(1, "series", 42, "Сёгун"))
	out := e.episodes(42, e.admin)
	src := out.Episodes[0].Sources
	if len(src) != 1 || src[0].Kind != "library" || src[0].Ref != "item:"+itoa(ids[0]) || src[0].Quality != "1080p" ||
		src[0].Meta.ItemID != ids[0] || src[0].Meta.FileID != fs[0].ID {
		t.Fatalf("%+v", src)
	}
}

func TestEpisodesFilterLibraryOnly(t *testing.T) {
	e := newTrackEnvWith(t, func(d *Deps) { d.LibraryOnly = true })
	e.series("Сёгун", "", 42)
	ti := e.track(1, "series", 42, "Сёгун")
	e.srcRows(ti)
	e.refresh(ti)
	if k := e.episodes(42, e.admin).kinds(0); len(k) != 1 || k[0] != "library" {
		t.Fatalf("kinds %v", k)
	}
}

func TestEpisodesNoOnlineForKid(t *testing.T) {
	e := newTrackEnv(t)
	e.series("Сёгун", "6+", 42)
	ti := e.track(1, "series", 42, "Сёгун")
	e.srcRows(ti)
	e.refresh(ti)
	for _, k := range e.episodes(42, e.kid("")).kinds(0) {
		if k == "online" {
			t.Fatal("kid sees online")
		}
	}
}

func TestEpisodesWatchedMerged(t *testing.T) {
	e := newTrackEnv(t)
	e.svc.WatchStates = []tracking.WatchStateFunc{
		func(context.Context, int64, string, int64) (map[[2]int]tracking.WatchState, error) {
			return map[[2]int]tracking.WatchState{{1, 1}: {PositionSec: 30}, {1, 2}: {Watched: true, PositionSec: 5}}, nil
		},
		func(context.Context, int64, string, int64) (map[[2]int]tracking.WatchState, error) {
			return map[[2]int]tracking.WatchState{{1, 1}: {Watched: true, PositionSec: 90}, {1, 2}: {PositionSec: 50}}, nil
		}}
	out := e.episodes(42, e.admin)
	a, b := out.Episodes[0], out.Episodes[1]
	if !a.Watched || a.PositionSec != 90 || !b.Watched || b.PositionSec != 50 {
		t.Fatalf("%+v %+v", a, b)
	}
}

func TestEpisodesNewFlag(t *testing.T) {
	e := newTrackEnv(t)
	e.series("Сёгун", "", 42)
	ti := e.track(1, "series", 42, "Сёгун")
	e.refresh(ti)
	ctx := context.Background()
	// episode 1: a fresh torrent; episode 2: only the library file
	if _, err := e.svc.Store.UpsertSources(ctx, ti.ID, "torrent", []tracking.Source{{Season: 1, Episode: 1, Ref: "t"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	out := e.episodes(42, e.admin)
	if !out.Episodes[0].New || out.Episodes[1].New {
		t.Fatalf("new %v %v", out.Episodes[0].New, out.Episodes[1].New)
	}
	// watched: no longer new
	e.svc.WatchStates = append(e.svc.WatchStates, func(context.Context, int64, string, int64) (map[[2]int]tracking.WatchState, error) {
		return map[[2]int]tracking.WatchState{{1, 1}: {Watched: true}}, nil
	})
	if out = e.episodes(42, e.admin); out.Episodes[0].New {
		t.Fatal("watched episode is new")
	}
	// a torrent first seen outside the feed window is not new
	if _, err := e.svc.Store.UpsertSources(ctx, ti.ID, "torrent", []tracking.Source{{Season: 1, Episode: 2, Ref: "old"}}, time.Now().AddDate(0, 0, -feedDays-3)); err != nil {
		t.Fatal(err)
	}
	if out = e.episodes(42, e.admin); out.Episodes[1].New {
		t.Fatal("old torrent is new")
	}
}

// TestEpisodesListLibraryOnlyEpisodes: a library file whose episode the
// TMDB list lacks (a special, absolute numbering, a stale list) is still
// an episode of the guide, in season/number order, named by its item.
func TestEpisodesListLibraryOnlyEpisodes(t *testing.T) {
	e := newTrackEnv(t)
	e.series("Сёгун", "", 42)
	for _, x := range []struct {
		season, episode int
		name            string
	}{{0, 3, "Спецвыпуск"}, {1, 7, "Седьмая"}} {
		p := filepath.Join(e.media, "Сёгун-extra-"+itoa(int64(x.season))+"-"+itoa(int64(x.episode))+".mkv")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := e.repo.UpsertEpisode(context.Background(), library.EpisodeInput{SeriesTitle: "Сёгун", SeriesTmdbID: 42,
			Season: x.season, Episode: x.episode, Title: x.name, File: library.FileInput{Path: p, Size: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	e.refresh(e.track(1, "series", 42, "Сёгун"))
	out := e.episodes(42, e.admin)
	var got []string
	for _, ep := range out.Episodes {
		got = append(got, itoa(int64(ep.Season))+"x"+itoa(int64(ep.Number))+" "+ep.Name+" "+ep.AirDate+" "+strings.Join(out.kinds(len(got)), ","))
	}
	today := time.Now().Format(time.DateOnly)
	want := []string{"0x3 Спецвыпуск  library", "1x1 Первая " + today + " library", "1x2 Вторая " + today + " library", "1x7 Седьмая  library"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("episodes\n%v\nwant\n%v", got, want)
	}
}

// TestEpisodesNewFollowsFeedRule: «новое» on a row agrees with «Вышло
// новое»: an episode that aired long ago is old news however fresh its
// torrent, and an episode already in the library does not turn new when it
// gets another source.
func TestEpisodesNewFollowsFeedRule(t *testing.T) {
	e := newTrackEnv(t)
	e.sk.eps[0].AirDate = time.Now().AddDate(0, 0, -60).Format(time.DateOnly)
	ti := e.track(1, "series", 42, "Сёгун")
	e.refresh(ti)
	ctx := context.Background()
	if _, err := e.svc.Store.UpsertSources(ctx, ti.ID, "torrent", []tracking.Source{{Season: 1, Episode: 1, Ref: "t"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if out := e.episodes(42, e.admin); out.Episodes[0].New {
		t.Fatal("an episode aired 60 days ago is new")
	}

	e2 := newTrackEnv(t)
	e2.series("Сёгун", "", 42)
	ti2 := e2.track(1, "series", 42, "Сёгун")
	e2.refresh(ti2)
	old := time.Now().AddDate(0, 0, -feedDays-5).UTC().Format(time.RFC3339)
	if _, err := e2.db.Exec(`UPDATE episode_sources SET first_seen=? WHERE kind='library'`, old); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.svc.Store.UpsertSources(ctx, ti2.ID, "pinned", []tracking.Source{{Season: 1, Episode: 1, Ref: "pin:1:0"}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if out := e2.episodes(42, e2.admin); out.Episodes[0].New {
		t.Fatal("a library episode with a fresh pin is new")
	}
}

// TestOnDemandRefreshSharedByConcurrentRequests: the guide asks for the
// episodes and the choice of an untracked title at once; one refresh serves
// both.
func TestOnDemandRefreshSharedByConcurrentRequests(t *testing.T) {
	e := newTrackEnv(t)
	e.sk.delay = 150 * time.Millisecond
	// the title exists (as a first visit leaves it), never refreshed
	if _, err := e.svc.Store.EnsureTitle(context.Background(), "series", 55, "", ""); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	codes := make([]int, 4)
	for i, path := range []string{"episodes", "choice", "episodes", "choice"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = e.do("GET", "/api/tracking/series/55/"+path, "", e.admin).Code
		}()
	}
	wg.Wait()
	for i, c := range codes {
		if c != 200 {
			t.Fatalf("request %d: %d", i, c)
		}
	}
	e.sk.mu.Lock()
	defer e.sk.mu.Unlock()
	if e.sk.calls != 1 {
		t.Fatalf("refreshes %d, want 1", e.sk.calls)
	}
}
