package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/tracking"
	"github.com/go-chi/chi/v5"
)

type coreTracking struct {
	*addonEnv
	deps Deps
	svc  *tracking.Service
}

// newCoreTracking: the addon env with a tracking service; the addon "tr"
// has the tracking permission, "hello" does not.
func newCoreTracking(t *testing.T) *coreTracking {
	c := &coreTracking{}
	c.addonEnv = newAddonEnv(t, func(d *Deps) {
		c.svc = &tracking.Service{Store: &tracking.Store{DB: d.DB}}
		d.Tracking = c.svc
		c.deps = *d
	})
	c.fake.mu.Lock()
	in := c.fake.installed["hello"]
	in.ID, in.Manifest.ID, in.Manifest.Permissions = "tr", "tr", []string{"tracking"}
	c.fake.installed["tr"], c.fake.tokens["tok-tr"], c.fake.status["tr"] = in, "tr", addons.StatusRunning
	c.fake.mu.Unlock()
	return c
}

func (c *coreTracking) call(tok, method, path, body string) *httptest.ResponseRecorder {
	return c.do(method, "/api/addon-core/v1"+path, body, nil, "Authorization", "Bearer "+tok, "Content-Type", "application/json")
}

func TestAddonCoreTracking_EnsureAndGet(t *testing.T) {
	c := newCoreTracking(t)
	body := `{"kind":"series","tmdbId":126308,"name":"Сёгун","poster":"/p.jpg"}`
	w := c.call("tok-tr", "POST", "/tracking/titles", body)
	if w.Code != 200 {
		t.Fatalf("ensure: %d %s", w.Code, w.Body)
	}
	var first addonkit.TrackingTitle
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil || first.ID == 0 || first.Kind != "series" ||
		first.TMDBID != 126308 || first.Name != "Сёгун" || first.Poster != "/p.jpg" {
		t.Fatalf("ensure: %+v %v", first, err)
	}
	// again (another name): the same title, the stored name kept
	w = c.call("tok-tr", "POST", "/tracking/titles", `{"kind":"series","tmdbId":126308,"name":"Другое"}`)
	var again addonkit.TrackingTitle
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &again) != nil || again.ID != first.ID || again.Name != "Сёгун" {
		t.Fatalf("ensure again: %d %s", w.Code, w.Body)
	}

	w = c.call("tok-tr", "GET", "/tracking/titles?kind=series&tmdbId=126308", "")
	var byTMDB addonkit.TrackingTitle
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &byTMDB) != nil || byTMDB != first {
		t.Fatalf("by tmdb: %d %s", w.Code, w.Body)
	}
	w = c.call("tok-tr", "GET", fmt.Sprintf("/tracking/titles/%d", first.ID), "")
	var byID addonkit.TrackingTitle
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &byID) != nil || byID != first {
		t.Fatalf("by id: %d %s", w.Code, w.Body)
	}

	// unknown: 404 «Тайтл не отслеживается»
	for _, p := range []string{"/tracking/titles?kind=movie&tmdbId=126308", "/tracking/titles?kind=series&tmdbId=7",
		fmt.Sprintf("/tracking/titles/%d", first.ID+100), fmt.Sprintf("/tracking/titles/%d/episodes", first.ID+100),
		fmt.Sprintf("/tracking/titles/%d/sources", first.ID+100)} {
		w := c.call("tok-tr", "GET", p, "")
		if w.Code != 404 || !strings.Contains(w.Body.String(), "Тайтл не отслеживается") {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
	if w := c.call("tok-tr", "POST", fmt.Sprintf("/tracking/titles/%d/kick", first.ID+100), ""); w.Code != 404 {
		t.Errorf("kick unknown: %d %s", w.Code, w.Body)
	}

	// malformed: 400, nothing made
	for _, b := range []string{`{"kind":"album","tmdbId":1}`, `{"kind":"movie","tmdbId":0}`, `{"kind":"movie"}`, `not json`} {
		if w := c.call("tok-tr", "POST", "/tracking/titles", b); w.Code != 400 {
			t.Errorf("ensure %s: %d %s", b, w.Code, w.Body)
		}
	}
	for _, p := range []string{"/tracking/titles?kind=album&tmdbId=1", "/tracking/titles?kind=movie", "/tracking/titles",
		"/tracking/titles/x", "/tracking/titles/0/episodes"} {
		if w := c.call("tok-tr", "GET", p, ""); w.Code != 400 {
			t.Errorf("%s: %d %s", p, w.Code, w.Body)
		}
	}
	var n int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM tracked_titles`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("titles %d %v", n, err)
	}
}

// fakeKick records the ids it is given.
type fakeKick struct {
	mu  sync.Mutex
	ids []int64
}

func (f *fakeKick) kick(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ids = append(f.ids, id)
}

func TestAddonCoreTracking_Kick(t *testing.T) {
	c := newCoreTracking(t)
	ti, err := c.svc.Store.EnsureTitle(context.Background(), "movie", 603, "Матрица", "")
	if err != nil {
		t.Fatal(err)
	}
	// the core's own handler with a recording kick in the service's place
	f := &fakeKick{}
	a := newAddonsAPI(c.deps)
	a.trackKick = f.kick
	r := chi.NewRouter()
	r.Route("/api/addon-core/v1", func(r chi.Router) {
		r.Use(a.requireToken)
		mountAddonCoreTracking(r, a)
	})
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/addon-core/v1/tracking/titles/%d/kick", ti.ID), nil)
	req.Header.Set("Authorization", "Bearer tok-tr")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 204 || w.Body.Len() != 0 {
		t.Fatalf("kick: %d %s", w.Code, w.Body)
	}
	if !slices.Equal(f.ids, []int64{ti.ID}) {
		t.Fatalf("kicked %v, want [%d]", f.ids, ti.ID)
	}

	// production: the service's own kick (the router's wiring answers 204)
	if w := c.call("tok-tr", "POST", fmt.Sprintf("/tracking/titles/%d/kick", ti.ID), ""); w.Code != 204 {
		t.Fatalf("router kick: %d %s", w.Code, w.Body)
	}
}

// putTrackingSources stores two episodes and three sources (neutral
// kinds) of ti.
func putTrackingSources(t *testing.T, st *tracking.Store, ti tracking.Title, itemID, fileID int64) {
	t.Helper()
	ctx := context.Background()
	if err := st.ReplaceEpisodes(ctx, ti.ID, []tracking.Episode{
		{Season: 1, Number: 1, Name: "Анджин", AirDate: "2024-02-27"},
		{Season: 1, Number: 2, Name: "Слуги двух господ", AirDate: "2024-02-27"}}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	idx := 3
	for _, s := range []struct {
		kind string
		srcs []tracking.Source
	}{
		{"library", []tracking.Source{{Season: 1, Episode: 1, Ref: fmt.Sprintf("file:%d", fileID), Provider: "library",
			Meta: tracking.SourceMeta{ItemID: itemID, FileID: fileID}}}},
		{"alpha", []tracking.Source{{Season: 1, Episode: 2, Ref: "alpha:1", Provider: "a1", Quality: "1080p", Labels: []string{"HDR"},
			Meta: tracking.SourceMeta{SourceID: "s-1", Voice: "Студия", Lang: "ru", Audio: "MVO", Studios: []string{"Студия"},
				Subs: []string{"en"}, FileIndex: &idx}}}},
		{"beta", []tracking.Source{{Season: 1, Episode: 2, Ref: "beta:1", Provider: "b1", Quality: "720p",
			Meta: tracking.SourceMeta{Provider: "b-host", Voice: "Оригинал", Lang: "ja", Audio: "Original"}}}},
	} {
		if _, err := st.UpsertSources(ctx, ti.ID, s.kind, s.srcs, now); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAddonCoreTracking_EpisodesSources(t *testing.T) {
	c := newCoreTracking(t)
	ti, err := c.svc.Store.EnsureTitle(context.Background(), "series", 126308, "Сёгун", "")
	if err != nil {
		t.Fatal(err)
	}
	putTrackingSources(t, c.svc.Store, ti, 11, 12)

	w := c.call("tok-tr", "GET", fmt.Sprintf("/tracking/titles/%d/episodes", ti.ID), "")
	var eps []addonkit.TrackingEpisode
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &eps) != nil {
		t.Fatalf("episodes: %d %s", w.Code, w.Body)
	}
	if want := []addonkit.TrackingEpisode{{TitleID: ti.ID, Season: 1, Number: 1, Name: "Анджин", AirDate: "2024-02-27"},
		{TitleID: ti.ID, Season: 1, Number: 2, Name: "Слуги двух господ", AirDate: "2024-02-27"}}; !slices.Equal(eps, want) {
		t.Fatalf("episodes %+v", eps)
	}
	for _, k := range []string{`"titleId"`, `"airDate"`, `"number"`} {
		if !strings.Contains(w.Body.String(), k) {
			t.Errorf("episodes: no %s in %s", k, w.Body)
		}
	}

	w = c.call("tok-tr", "GET", fmt.Sprintf("/tracking/titles/%d/sources", ti.ID), "")
	var srcs []addonkit.TrackingSource
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &srcs) != nil || len(srcs) != 3 {
		t.Fatalf("sources: %d %s", w.Code, w.Body)
	}
	// every kind, in season, episode, kind, ref order
	if srcs[0].Kind != "library" || srcs[0].Meta.ItemID != 11 || srcs[0].Meta.FileID != 12 || srcs[0].Episode != 1 {
		t.Fatalf("library row %+v", srcs[0])
	}
	a, b := srcs[1], srcs[2]
	if a.Kind != "alpha" || a.Ref != "alpha:1" || a.Provider != "a1" || a.Quality != "1080p" || !slices.Equal(a.Labels, []string{"HDR"}) ||
		a.Meta.SourceID != "s-1" || a.Meta.Voice != "Студия" || a.Meta.Lang != "ru" || a.Meta.Audio != "MVO" ||
		!slices.Equal(a.Meta.Studios, []string{"Студия"}) || !slices.Equal(a.Meta.Subs, []string{"en"}) ||
		a.Meta.FileIndex == nil || *a.Meta.FileIndex != 3 || a.TitleID != ti.ID || a.FirstSeen.IsZero() || a.LastSeen.IsZero() {
		t.Fatalf("alpha row %+v", a)
	}
	if b.Kind != "beta" || b.Meta.Provider != "b-host" || b.Meta.FileIndex != nil {
		t.Fatalf("beta row %+v", b)
	}
	for _, k := range []string{`"fileIndex":3`, `"providerId":"b-host"`, `"sourceId"`, `"firstSeen"`, `"lastSeen"`, `"titleId"`} {
		if !strings.Contains(w.Body.String(), k) {
			t.Errorf("sources: no %s in %s", k, w.Body)
		}
	}

	// a title without any: [] (not null)
	other, _ := c.svc.Store.EnsureTitle(context.Background(), "movie", 603, "Матрица", "")
	for _, p := range []string{"episodes", "sources"} {
		w := c.call("tok-tr", "GET", fmt.Sprintf("/tracking/titles/%d/%s", other.ID, p), "")
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
			t.Errorf("%s of an empty title: %d %s", p, w.Code, w.Body)
		}
	}
}

func TestAddonCoreTracking_NoPermission(t *testing.T) {
	c := newCoreTracking(t)
	for _, rt := range [][3]string{{"POST", "/tracking/titles", `{"kind":"movie","tmdbId":603}`},
		{"GET", "/tracking/titles?kind=movie&tmdbId=603", ""}, {"GET", "/tracking/titles/1", ""},
		{"POST", "/tracking/titles/1/kick", ""}, {"GET", "/tracking/titles/1/episodes", ""}, {"GET", "/tracking/titles/1/sources", ""}} {
		w := c.call("tok-hello", rt[0], rt[1], rt[2])
		if w.Code != 403 {
			t.Errorf("%s %s: %d %s", rt[0], rt[1], w.Code, w.Body)
		}
	}
	var n int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM tracked_titles`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("a title was made without the permission: %d %v", n, err)
	}

	// a server without tracking: 503, after the permission check
	e := newAddonEnv(t)
	e.fake.mu.Lock()
	in := e.fake.installed["hello"]
	in.ID, in.Manifest.ID, in.Manifest.Permissions = "tr", "tr", []string{"tracking"}
	e.fake.installed["tr"], e.fake.tokens["tok-tr"] = in, "tr"
	e.fake.mu.Unlock()
	for _, tok := range []string{"tok-tr", "tok-hello"} {
		w := e.do("GET", "/api/addon-core/v1/tracking/titles/1", "", nil, "Authorization", "Bearer "+tok)
		want := http.StatusServiceUnavailable
		if tok == "tok-hello" {
			want = http.StatusForbidden
		}
		if w.Code != want {
			t.Errorf("no tracking, %s: %d %s", tok, w.Code, w.Body)
		}
	}
}

func TestAddonCoreTracking_NoInternalIDs(t *testing.T) {
	c := newCoreTracking(t)
	ctx := context.Background()
	ti, err := c.svc.Store.EnsureTitle(ctx, "series", 126308, "Сёгун", "")
	if err != nil {
		t.Fatal(err)
	}
	sh, ms, ad := int64(5), int64(6), int64(7)
	if err := c.svc.Store.SetExternalIDs(ctx, ti.ID, &sh, &ms, &ad); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{fmt.Sprintf("/tracking/titles/%d", ti.ID), "/tracking/titles?kind=series&tmdbId=126308"} {
		w := c.call("tok-tr", "GET", p, "")
		var m map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &m) != nil {
			t.Fatalf("%s: %d %s", p, w.Code, w.Body)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if want := []string{"cert", "id", "imdbId", "kind", "name", "poster", "status", "tmdbId"}; !slices.Equal(keys, want) {
			t.Fatalf("%s: keys %v, want %v", p, keys, want)
		}
	}
	w := c.call("tok-tr", "POST", "/tracking/titles", `{"kind":"series","tmdbId":126308}`)
	for _, k := range []string{"shikimori", "myShows", "myshows", "aniDb", "anidb", "nextCheck", "refreshed"} {
		if strings.Contains(strings.ToLower(w.Body.String()), strings.ToLower(k)) {
			t.Errorf("ensure answer names %s: %s", k, w.Body)
		}
	}
}
