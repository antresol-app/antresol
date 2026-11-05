package api

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// TestSaveSettingsKeepsUnknownKeys: app_settings is one JSON row shared by
// every build that opens the database. A save keeps the keys another
// build wrote (the sources build's upload limit, stream cache and online
// switch survive a PUT on core), and a key of this build's own is
// written as this build has it.
func TestSaveSettingsKeepsUnknownKeys(t *testing.T) {
	e := newSettingsEnv(t)
	const row = `{"uploadLimited":true,"uploadLimitKBs":512,"streamCacheGB":33,"onlineEnabled":false,"packKey":{"a":[1,2]},"remoteQuality":"720"}`
	if _, err := e.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)`, appSettingsKey, row); err != nil {
		t.Fatal(err)
	}
	w := e.do("PUT", "/api/settings", `{"quality":1080,"voice":"dub","remoteQuality":"480"}`, e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	got := storedAppSettings(t, e)
	for k, want := range map[string]any{"uploadLimited": true, "uploadLimitKBs": 512.0, "streamCacheGB": 33.0,
		"onlineEnabled": false, "packKey": map[string]any{"a": []any{1.0, 2.0}}, "remoteQuality": "480"} {
		if !reflect.DeepEqual(got[k], want) {
			t.Errorf("%s = %#v, want %#v", k, got[k], want)
		}
	}
}

// TestSaveSettingsClearsOwnKeys: an omitempty key this build defines is
// cleared by leaving it out, never brought back from the stored row (the
// merge copies only keys this build does not define).
func TestSaveSettingsClearsOwnKeys(t *testing.T) {
	e := newSettingsEnv(t)
	ctx := context.Background()
	s := AppSettings{Mail: MailSettings{Host: "smtp.example.org", Port: 587, Username: "u", Password: "secret", From: "u@example.org"}}
	if err := saveAppSettings(ctx, e.db, s); err != nil {
		t.Fatal(err)
	}
	s.Mail.Password = ""
	if err := saveAppSettings(ctx, e.db, s); err != nil {
		t.Fatal(err)
	}
	mail, _ := storedAppSettings(t, e)["mail"].(map[string]any)
	if _, ok := mail["password"]; ok || mail["host"] != "smtp.example.org" {
		t.Fatalf("mail %v", mail)
	}

	// a top-level omitempty key of this build: left out of the new
	// value, it is dropped, while a key of another build stays
	stored := []byte(`{"own":"x","other":"y","kept":1}`)
	out, err := mergeForeignKeys(stored, []byte(`{"kept":2}`), map[string]bool{"own": true, "kept": true})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(out, &m)
	if want := map[string]any{"other": "y", "kept": 2.0}; !reflect.DeepEqual(m, want) {
		t.Fatalf("merged %v, want %v", m, want)
	}
}

// TestAppSettingsKeysCoverStruct: the keys this build owns are every JSON
// name of AppSettings, the omitempty ones included.
func TestAppSettingsKeysCoverStruct(t *testing.T) {
	keys := ownedKeys(reflect.TypeOf(AppSettings{}))
	for _, k := range []string{"remoteQuality", "remoteMusic", "autoSubtitles", "subtitleLangs", "languages",
		"metadataLanguage", "mail", "trickplay", "trickplayBudgetGB"} {
		if !keys[k] {
			t.Errorf("%s missing from %v", k, keys)
		}
	}
	type inner struct {
		A string `json:"a,omitempty"`
		B int    `json:"-"`
		C int
	}
	type outer struct {
		inner
		D string `json:"d"`
		e int
	}
	if got, want := ownedKeys(reflect.TypeOf(outer{})), map[string]bool{"a": true, "C": true, "d": true}; !reflect.DeepEqual(got, want) {
		t.Fatalf("keys %v, want %v", got, want)
	}
}

func storedAppSettings(t *testing.T, e *settingsEnv) map[string]any {
	t.Helper()
	var raw string
	if err := e.db.QueryRow(`SELECT value FROM settings WHERE key=?`, appSettingsKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return m
}
