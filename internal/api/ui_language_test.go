package api

import (
	"database/sql"
	"encoding/json"
	"testing"
)

func TestUILanguageFollowsTheProfile(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db })
	get := func() map[string]string {
		var m map[string]string
		w := e.do("GET", "/api/me/ui-language", "", e.admin)
		if w.Code != 200 {
			t.Fatalf("get %d %s", w.Code, w.Body)
		}
		json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	// no choice: the household's languages (Russian first by default)
	if m := get(); m["language"] != "" || m["effective"] != "ru" {
		t.Fatalf("default %v", m)
	}
	// the profile's own audio languages come first
	if w := e.do("PUT", "/api/me/playback-prefs", `{"audioLangs":["en","ru"],"subMode":"off","subLangs":[],"subSize":"m","subBackground":"translucent"}`, e.admin); w.Code != 200 {
		t.Fatalf("prefs %d %s", w.Code, w.Body)
	}
	if m := get(); m["effective"] != "en" {
		t.Fatalf("from prefs %v", m)
	}
	// an explicit choice wins; "" goes back
	if w := e.do("PUT", "/api/me/ui-language", `{"language":"ru"}`, e.admin); w.Code != 200 {
		t.Fatalf("put %d %s", w.Code, w.Body)
	}
	if m := get(); m["language"] != "ru" || m["effective"] != "ru" {
		t.Fatalf("chosen %v", m)
	}
	if w := e.do("PUT", "/api/me/ui-language", `{"language":"de"}`, e.admin); w.Code != 400 {
		t.Fatalf("unknown %d", w.Code)
	}
	e.do("PUT", "/api/me/ui-language", `{"language":""}`, e.admin)
	if m := get(); m["language"] != "" || m["effective"] != "en" {
		t.Fatalf("reset %v", m)
	}
}
