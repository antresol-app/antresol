package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/addonkit"
	"github.com/antresol-app/antresol/internal/addons"
	"github.com/antresol-app/antresol/internal/library"
)

// contEnv is a library with fake addons answering home.continue.
type contEnv struct {
	*libEnv
	fake *fakeAddons

	mu    sync.Mutex
	calls map[string][]pendingCall // addon id → its home.continue calls
	// remote: the viewer is outside the home network (ClientIsRemote)
	remote atomic.Bool
}

func newContEnv(t *testing.T) *contEnv {
	t.Helper()
	e := &contEnv{calls: map[string][]pendingCall{},
		fake: &fakeAddons{installed: map[string]addons.Installed{}, status: map[string]addons.Status{},
			sock: map[string]string{}, tokens: map[string]string{}, webRoot: t.TempDir()}}
	e.libEnv = newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.AddonHooks = &addons.Hooks{T: e.fake, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
		d.ClientIsRemote = func(*http.Request) bool { return e.remote.Load() }
	})
	return e
}

// addon starts addon id (access "" = admins) declaring home.continue;
// answer writes its reply.
func (e *contEnv) addon(id, access string, answer func(w http.ResponseWriter, r *http.Request)) {
	e.t.Helper()
	dir, err := os.MkdirTemp("/tmp", "hc")
	if err != nil {
		e.t.Fatal(err)
	}
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		e.t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/hooks/home.continue" {
			http.NotFound(w, r)
			return
		}
		e.mu.Lock()
		e.calls[id] = append(e.calls[id], pendingCall{body: b, user: r.Header.Get("X-Antresol-User")})
		e.mu.Unlock()
		answer(w, r)
	})}
	go srv.Serve(ln)
	e.t.Cleanup(func() { srv.Close(); os.RemoveAll(dir) })
	e.fake.mu.Lock()
	e.fake.installed[id] = addons.Installed{ID: id, Version: "1.0.0", Enabled: true,
		Manifest: addons.Manifest{ID: id, Version: "1.0.0", Contract: addons.ContractVersion, Access: access, Hooks: []string{"home.continue"}}}
	e.fake.status[id] = addons.StatusRunning
	e.fake.sock[id] = sock
	e.fake.mu.Unlock()
}

func (e *contEnv) callsOf(id string) []pendingCall {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]pendingCall(nil), e.calls[id]...)
}

// contCards answers with these cards.
func contCards(cs ...addonkit.ContinueCard) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(addonkit.ContinueAnswer{Cards: cs})
	}
}

// ccard is a rated movie card of key, last watched at.
func ccard(key string, tmdbID int64, rating string, at time.Time) addonkit.ContinueCard {
	return addonkit.ContinueCard{Key: key, Kind: "movie", TMDBID: tmdbID, Title: "Кино " + key, Rating: rating,
		Progress: 0.5, ResumeSec: 600, Href: "/addons/seed/watch/" + key, UpdatedAt: at}
}

type contCard struct {
	ID        int64   `json:"id"`
	Key       string  `json:"key"`
	AddonID   string  `json:"addonId"`
	Kind      string  `json:"kind"`
	Title     string  `json:"title"`
	Subtitle  string  `json:"subtitle"`
	Poster    string  `json:"poster"`
	Href      string  `json:"href"`
	Progress  float64 `json:"progress"`
	ResumeSec float64 `json:"resumeSec"`
	TmdbKind  string  `json:"tmdbKind"`
	TmdbID    int     `json:"tmdbId"`
	Season    int     `json:"season"`
	Episode   int     `json:"episode"`
}

// cont is the «Продолжить» shelf of path for c.
func (e *contEnv) cont(path string, c *http.Cookie) []contCard {
	e.t.Helper()
	w := e.do("GET", path, "", c)
	var out struct {
		Shelves []struct {
			ID    string     `json:"id"`
			Cards []contCard `json:"cards"`
		} `json:"shelves"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); w.Code != 200 || err != nil || out.Shelves[0].ID != "continue" {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	return out.Shelves[0].Cards
}

// titles lists the shelf's titles in order.
func titles(cs []contCard) string {
	var s []string
	for _, c := range cs {
		s = append(s, c.Title)
	}
	return strings.Join(s, ", ")
}

// tmdbMovie puts a movie with a TMDB id and a rating in the library.
func (e *contEnv) tmdbMovie(title string, tmdbID int64, rating string) int64 {
	e.t.Helper()
	p := filepath.Join(e.media, title+".mkv")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: title, Year: 2000, Rating: rating, TmdbID: tmdbID,
		File: library.FileInput{Path: p, Size: 1}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// The addons' cards join the core's own «Продолжить» by when they were
// last watched.
func TestHomeContinue_MergedByUpdatedAt(t *testing.T) {
	e := newContEnv(t)
	lib, _ := e.movie("Брат", "12+", "0123456789abcdef")
	now := time.Now()
	e.prog.Now = func() time.Time { return now.Add(-time.Hour) }
	if err := e.prog.Save(context.Background(), 1, lib, 1000, 4000); err != nil {
		t.Fatal(err)
	}
	e.addon("seed", "", contCards(ccard("old", 11, "12+", now.Add(-2*time.Hour)), ccard("new", 12, "12+", now)))
	got := e.cont("/api/home?addons=1", e.admin)
	if titles(got) != "Кино new, Брат, Кино old" {
		t.Fatalf("order: %s", titles(got))
	}
	if c := got[0]; c.ID != 0 || c.Kind != "movie" || c.TmdbKind != "movie" || c.TmdbID != 12 || c.Progress != 0.5 || c.ResumeSec != 600 ||
		c.Href != "/addons/seed/watch/new" || c.AddonID != "seed" {
		t.Fatalf("addon card %+v", c)
	}
	if got[1].Key != "" || got[1].AddonID != "" || got[1].Href != "" {
		t.Fatalf("library card gained addon fields: %+v", got[1])
	}
}

// The core namespaces each key as addon:<id>:<key>; a card without a key,
// or of a kind other than movie or series, is dropped.
func TestHomeContinue_KeyNamespaced(t *testing.T) {
	e := newContEnv(t)
	now := time.Now()
	album := ccard("album", 3, "12+", now)
	album.Kind = "album"
	ser := ccard("s", 4, "12+", now)
	ser.Kind, ser.Season, ser.Episode, ser.Subtitle = "series", 2, 5, "S2 E5"
	e.addon("seed", "", contCards(ccard("k1", 1, "12+", now), ccard("", 2, "12+", now), album, ser))
	got := e.cont("/api/home?addons=1", e.admin)
	if len(got) != 2 {
		t.Fatalf("cards %+v", got)
	}
	keys := map[string]contCard{}
	for _, c := range got {
		keys[c.Key] = c
	}
	if _, ok := keys["addon:seed:k1"]; !ok {
		t.Fatalf("keys %+v", got)
	}
	if s, ok := keys["addon:seed:s"]; !ok || s.Kind != "series" || s.TmdbKind != "series" || s.Season != 2 || s.Episode != 5 || s.Subtitle != "S2 E5" {
		t.Fatalf("series card %+v", got)
	}
}

// A poster is an image name POST /images gave; any other value (an
// outside URL) becomes "".
func TestHomeContinue_PosterImageNameOnly(t *testing.T) {
	e := newContEnv(t)
	now := time.Now()
	name := "0123456789abcdef0123456789abcdef01234567.jpg"
	a, b := ccard("a", 1, "12+", now), ccard("b", 2, "12+", now.Add(-time.Minute))
	a.Poster, b.Poster = name, "https://example.org/p.jpg"
	e.addon("seed", "", contCards(a, b))
	got := e.cont("/api/home?addons=1", e.admin)
	if len(got) != 2 || got[0].Poster != "/api/images/"+name || got[1].Poster != "" {
		t.Fatalf("posters %+v", got)
	}
}

// href is kept only when it is a path of the app: it starts with / but not
// //, and holds no backslash and no control character (a browser reads
// /\ as //). Another href is dropped; the card stays.
func TestHomeContinue_HrefChecked(t *testing.T) {
	e := newContEnv(t)
	now := time.Now()
	hrefs := []string{"/addons/seed/w?t=5", "//x.example", "http://x.example", "/\\x.example", "/a\\b", "/a\nb", "/a\tb", "/a\x7fb", "/a\x00b", "javascript:alert(1)"}
	var cs []addonkit.ContinueCard
	for i, h := range hrefs {
		c := ccard(string(rune('a'+i)), int64(i+1), "12+", now.Add(-time.Duration(i)*time.Minute))
		c.Href = h
		cs = append(cs, c)
	}
	e.addon("seed", "", contCards(cs...))
	got := e.cont("/api/home?addons=1", e.admin)
	if len(got) != len(hrefs) {
		t.Fatalf("%d cards, want %d", len(got), len(hrefs))
	}
	if got[0].Href != "/addons/seed/w?t=5" {
		t.Fatalf("a good href was dropped: %+v", got[0])
	}
	for _, c := range got[1:] {
		if c.Href != "" {
			t.Errorf("href %q kept", c.Href)
		}
	}
}

// A kid's Home never asks the addons.
func TestHomeContinue_NotForKids(t *testing.T) {
	e := newContEnv(t)
	e.addon("seed", "adults", contCards(ccard("k", 1, "0+", time.Now())))
	kid := e.kid("")
	if got := e.cont("/api/home?addons=1", kid); len(got) != 0 {
		t.Fatalf("kid got %+v", got)
	}
	if n := len(e.callsOf("seed")); n != 0 {
		t.Fatalf("the hook was called %d times for a kid", n)
	}
}

// A hung addon costs Home its 500 ms at most, and is left out; the others'
// cards are still there.
func TestHomeContinue_HungAddonLeftOut(t *testing.T) {
	e := newContEnv(t)
	e.addon("fast", "", contCards(ccard("f", 1, "12+", time.Now())))
	e.addon("slow", "", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
			return
		}
		contCards(ccard("s", 2, "12+", time.Now()))(w, r)
	})
	e.cont("/api/home", e.admin) // warm the router and the database
	start := time.Now()
	got := e.cont("/api/home?addons=1", e.admin)
	if d := time.Since(start); d >= 600*time.Millisecond {
		t.Fatalf("home took %v", d)
	}
	if len(got) != 1 || got[0].Key != "addon:fast:f" {
		t.Fatalf("cards %+v", got)
	}
	if n := len(e.callsOf("slow")); n != 1 {
		t.Fatalf("slow called %d times", n)
	}
}

// Only a Home that asks with addons=1 calls the hook, with body {} and the
// profile in the user header.
func TestHomeContinue_OnlyWithAddonsParam(t *testing.T) {
	e := newContEnv(t)
	e.addon("seed", "", contCards(ccard("k", 1, "12+", time.Now())))
	if got := e.cont("/api/home", e.admin); len(got) != 0 || len(e.callsOf("seed")) != 0 {
		t.Fatalf("plain home: %+v, %d calls", got, len(e.callsOf("seed")))
	}
	if got := e.cont("/api/home?addons=1", e.admin); len(got) != 1 {
		t.Fatalf("addons=1: %+v", got)
	}
	cs := e.callsOf("seed")
	if len(cs) != 1 || string(cs[0].body) != "{}" || !strings.Contains(cs[0].user, `"id":1`) || !strings.Contains(cs[0].user, `"isAdmin":true`) {
		t.Fatalf("calls %+v", cs)
	}
}

// An addon for admins (the default access) is never asked for a non-admin
// adult, the same rule as the bundles; one for adults is, with the
// profile's limit in the header.
func TestHomeContinue_AdminsOnlyAddonNotForAdults(t *testing.T) {
	e := newContEnv(t)
	e.addon("admins", "", contCards(ccard("a", 1, "6+", time.Now())))
	e.addon("family", "adults", contCards(ccard("f", 2, "6+", time.Now())))
	adult := e.login(`{"name":"Петя","maxRating":"12+"}`)
	got := e.cont("/api/home?addons=1", adult)
	if len(got) != 1 || got[0].AddonID != "family" {
		t.Fatalf("adult got %+v", got)
	}
	if n := len(e.callsOf("admins")); n != 0 {
		t.Fatalf("the admins' addon was asked %d times for an adult", n)
	}
	if fc := e.callsOf("family"); len(fc) != 1 || !strings.Contains(fc[0].user, `"maxRating":"12+"`) || !strings.Contains(fc[0].user, `"isAdmin":false`) {
		t.Fatalf("family calls %+v", fc)
	}
	if got := e.cont("/api/home?addons=1", e.admin); len(got) != 2 {
		t.Fatalf("admin got %+v", got)
	}
}

// A card for a library title rated above the profile's limit is dropped,
// whatever rating the addon sent.
func TestHomeContinue_CoreDropsOverLimit(t *testing.T) {
	e := newContEnv(t)
	e.tmdbMovie("Взрослое", 7, "18+")
	e.addon("seed", "adults", contCards(ccard("over", 7, "6+", time.Now())))
	teen := e.login(`{"name":"Петя","maxRating":"12+"}`)
	if got := e.cont("/api/home?addons=1", teen); len(got) != 0 {
		t.Fatalf("12+ got %+v", got)
	}
	if got := e.cont("/api/home?addons=1", e.admin); len(got) != 1 {
		t.Fatalf("admin got %+v", got)
	}
}

// The core's item rating decides, not the card's: a 6+ library title is
// kept for a 12+ profile though the addon called it 18+.
func TestHomeContinue_LibraryRatingWins(t *testing.T) {
	e := newContEnv(t)
	e.tmdbMovie("Мультик", 8, "6+")
	ser := ccard("series", 8, "6+", time.Now().Add(-time.Minute))
	ser.Kind = "series" // TMDB ids of movies and series are apart: no item of this kind
	e.addon("seed", "adults", contCards(ccard("kids", 8, "18+", time.Now()), ser))
	teen := e.login(`{"name":"Петя","maxRating":"12+"}`)
	got := e.cont("/api/home?addons=1", teen)
	if len(got) != 2 || got[0].Key != "addon:seed:kids" || got[1].Key != "addon:seed:series" {
		t.Fatalf("12+ got %+v", got)
	}
}

// An unrated card of a title the library does not have is dropped for a
// rating-limited profile and kept for an unlimited one.
func TestHomeContinue_UnratedDroppedWhenLimited(t *testing.T) {
	e := newContEnv(t)
	now := time.Now()
	e.addon("seed", "adults", contCards(ccard("unrated", 99, "", now), ccard("rated", 98, "12+", now.Add(-time.Minute)),
		ccard("high", 97, "16+", now.Add(-2*time.Minute))))
	teen := e.login(`{"name":"Петя","maxRating":"12+"}`)
	if got := e.cont("/api/home?addons=1", teen); titles(got) != "Кино rated" {
		t.Fatalf("12+ got %s", titles(got))
	}
	if got := e.cont("/api/home?addons=1", e.admin); len(got) != 3 {
		t.Fatalf("admin got %s", titles(got))
	}
}

// Outside the home network the profile in the header also carries the
// height «Качество вне дома» caps «Авто» at; at home it does not.
func TestHomeContinue_RemoteCapInHeader(t *testing.T) {
	e := newContEnv(t)
	e.addon("capped", "", contCards(ccard("k", 1, "12+", time.Now())))
	e.cont("/api/home?addons=1", e.admin)
	e.remote.Store(true)
	e.cont("/api/home?addons=1", e.admin)
	cs := e.callsOf("capped")
	if len(cs) != 2 || strings.Contains(cs[0].user, "maxHeight") || !strings.Contains(cs[1].user, `"maxHeight":1080`) {
		t.Fatalf("calls %+v", cs)
	}
}
