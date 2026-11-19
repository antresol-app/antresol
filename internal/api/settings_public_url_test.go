package api

import (
	"encoding/json"
	"testing"

	"github.com/antresol-app/antresol/internal/publicurl"
)

// TestSettingsPublicURL: «Адрес сервера» is saved trimmed and applied at
// once; an address that is not an http(s) link is refused; the server's
// VAULT_PUBLIC_URL is reported, and it is the address in force.
func TestSettingsPublicURL(t *testing.T) {
	e := newSettingsEnv(t)
	var s struct {
		PublicURL        string `json:"publicUrl"`
		PublicURLFromEnv string `json:"publicUrlFromEnv"`
	}
	w := e.do("PUT", "/api/settings", `{"quality":1080,"voice":"dub","streamCacheGB":20,"publicUrl":" https://media.example.org/ "}`, e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &s)
	if s.PublicURL != "https://media.example.org" || s.PublicURLFromEnv != "" {
		t.Fatalf("saved %+v", s)
	}
	e.mu.Lock()
	last := e.applied[len(e.applied)-1]
	e.mu.Unlock()
	if last.PublicURL != "https://media.example.org" {
		t.Errorf("applied %q", last.PublicURL)
	}
	w = e.do("PUT", "/api/settings", `{"quality":1080,"voice":"dub","streamCacheGB":20,"publicUrl":"media.example.org"}`, e.admin)
	var out struct{ Error string }
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 400 || !russian(out.Error) {
		t.Fatalf("invalid: %d %q", w.Code, out.Error)
	}
	// a client that leaves it out keeps it
	if w := e.do("PUT", "/api/settings", `{"quality":1080,"voice":"dub","streamCacheGB":20}`, e.admin); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &s)
	if s.PublicURL != "https://media.example.org" {
		t.Fatalf("kept %q", s.PublicURL)
	}
}

// TestSettingsPublicURLFromEnv: with VAULT_PUBLIC_URL the answer says so.
func TestSettingsPublicURLFromEnv(t *testing.T) {
	a := &settingsAPI{public: publicurl.New("https://env.example.org/")}
	v := settingsView{}
	v.PublicURLFromEnv = a.public.Env()
	if v.PublicURLFromEnv != "https://env.example.org" {
		t.Fatalf("%q", v.PublicURLFromEnv)
	}
}
