package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/extsvc"
)

type externalEnv struct {
	*env
	admin   *http.Cookie
	sw      *extsvc.Switches
	changed map[extsvc.ID]bool
}

func newExternalEnv(t *testing.T, envLocks map[extsvc.ID]bool) *externalEnv {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st, ss := auth.NewStore(d), auth.NewSessions(d)
	e := &externalEnv{env: &env{db: d, t: t, store: st}, sw: extsvc.New(envLocks), changed: map[extsvc.ID]bool{}}
	e.h = NewRouter(Deps{Users: st, Sessions: ss, DB: d, External: e.sw,
		ExternalChanged: func(id extsvc.ID, on bool) { e.changed[id] = on }})
	e.admin = e.setup()
	return e
}

func (e *externalEnv) view(method, body string) externalView {
	e.t.Helper()
	w := e.do(method, "/api/settings/external", body, e.admin)
	if w.Code != http.StatusOK {
		e.t.Fatalf("%s: %d %s", method, w.Code, w.Body)
	}
	var v externalView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (v externalView) of(id extsvc.ID) externalService {
	for _, s := range v.Services {
		if s.ID == id {
			return s
		}
	}
	return externalService{}
}

// TestExternalSettings: the defaults before any choice, with the one-time
// notice; a choice applies to the switches at once and tells the server;
// the environment's lock wins and shows; closing the notice keeps it closed.
func TestExternalSettings(t *testing.T) {
	e := newExternalEnv(t, map[extsvc.ID]bool{extsvc.Lyrics: false})
	v := e.view("GET", "")
	if v.Chosen || !v.Notice || len(v.Services) != len(extsvc.All) {
		t.Fatalf("fresh: %+v", v)
	}
	for _, id := range extsvc.All {
		s := v.of(id)
		want := extsvc.Default(id) && id != extsvc.Lyrics
		if s.On != want || s.Default != extsvc.Default(id) || s.Locked != (id == extsvc.Lyrics) {
			t.Fatalf("%s fresh: %+v", id, s)
		}
	}

	v = e.view("PUT", `{"services":{"wikidata":false,"imdb":true,"lyrics":true}}`)
	if !v.Chosen || v.Notice {
		t.Fatalf("chosen: %+v", v)
	}
	if v.of(extsvc.Wikidata).On || !v.of(extsvc.IMDb).On || v.of(extsvc.Lyrics).On || v.of(extsvc.Segments).On != extsvc.Default(extsvc.Segments) {
		t.Fatalf("after the choice: %+v", v.Services)
	}
	if e.sw.On(extsvc.Wikidata) || !e.sw.On(extsvc.IMDb) || e.sw.On(extsvc.Lyrics) {
		t.Fatal("the switches did not change at once")
	}
	if on, ok := e.changed[extsvc.Wikidata]; !ok || on {
		t.Fatalf("changed: %v", e.changed)
	}
	if _, ok := e.changed[extsvc.Lyrics]; ok {
		t.Fatal("a locked service was reported as changed")
	}
	// a partial save keeps the others; the stored choice survives a restart
	v = e.view("PUT", `{"services":{"wikidata":true}}`)
	if !v.of(extsvc.Wikidata).On || !v.of(extsvc.IMDb).On {
		t.Fatalf("partial: %+v", v.Services)
	}
	c, err := LoadExternalChoice(t.Context(), e.db)
	if err != nil || c[extsvc.IMDb] == nil || !*c[extsvc.IMDb] || c[extsvc.Segments] != nil {
		t.Fatalf("stored: %v %v", c, err)
	}

	if w := e.do("PUT", "/api/settings/external", `{"services":{"nope":true}}`, e.admin); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown id: %d", w.Code)
	}
}

func TestExternalNoticeDismissed(t *testing.T) {
	e := newExternalEnv(t, nil)
	v := e.view("PUT", `{"dismissNotice":true}`)
	if v.Notice || v.Chosen {
		t.Fatalf("dismissed: %+v", v)
	}
	if v = e.view("GET", ""); v.Notice {
		t.Fatal("the notice came back")
	}
}

func TestExternalSettingsAdminOnly(t *testing.T) {
	e := newExternalEnv(t, nil)
	w := e.do("POST", "/api/users", `{"name":"Мама"}`, e.admin)
	if w.Code != 201 {
		t.Fatal(w.Body)
	}
	var u auth.User
	json.Unmarshal(w.Body.Bytes(), &u)
	b, _ := json.Marshal(u.ID)
	user := session(e.do("POST", "/api/login", `{"userId":`+string(b)+`}`, nil))
	for _, m := range []string{"GET", "PUT"} {
		if w := e.do(m, "/api/settings/external", `{"services":{"imdb":true}}`, user); w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", m, w.Code)
		}
	}
}
