package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
)

type settingsEnv struct {
	*env
	admin   *http.Cookie
	mu      sync.Mutex
	applied []AppSettings
	// settingsEnvFull: the sources build's release preferences
	settingsEnvFull
}

func newSettingsEnv(t *testing.T) *settingsEnv {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st, ss := auth.NewStore(d), auth.NewSessions(d)
	e := &settingsEnv{env: &env{db: d, t: t, store: st}}
	deps := Deps{Users: st, Sessions: ss, DB: d,
		ApplySettings: func(s AppSettings) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.applied = append(e.applied, s)
		}}
	e.withFull(t, &deps)
	e.h = NewRouter(deps)
	e.admin = e.setup()
	return e
}

func TestSettingsRemoteQuality(t *testing.T) {
	e := newSettingsEnv(t)
	var s struct {
		RemoteQuality string `json:"remoteQuality"`
	}
	json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &s)
	if s.RemoteQuality != "1080" {
		t.Fatalf("default %q", s.RemoteQuality)
	}
	w := e.do("PUT", "/api/settings", `{"quality":1080,"voice":"dub","streamCacheGB":20,"remoteQuality":"480"}`, e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &s)
	if s.RemoteQuality != "480" {
		t.Fatalf("saved %q", s.RemoteQuality)
	}
	w = e.do("PUT", "/api/settings", `{"quality":1080,"voice":"dub","streamCacheGB":20,"remoteQuality":"8k"}`, e.admin)
	var out struct{ Error string }
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 400 || !russian(out.Error) {
		t.Fatalf("invalid: %d %q", w.Code, out.Error)
	}
}
