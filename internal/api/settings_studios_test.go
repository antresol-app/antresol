package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestSettingsStudiosBothEditions: «Любимые озвучки» are a core setting:
// stored cleaned, kept by a PUT that leaves them out, refused above 30.
// The body carries the sources build's required fields (the core build
// ignores them).
func TestSettingsStudiosBothEditions(t *testing.T) {
	e := newSettingsEnv(t)
	put := func(extra string) int {
		return e.do("PUT", "/api/settings", `{"quality":1080,"voice":"dub","streamCacheGB":20`+extra+`}`, e.admin).Code
	}
	studios := func() []string {
		var s struct {
			Studios []string `json:"studios"`
		}
		json.Unmarshal(e.do("GET", "/api/settings", "", e.admin).Body.Bytes(), &s)
		return s.Studios
	}
	if got := studios(); got == nil || len(got) != 0 {
		t.Fatalf("default %#v", got)
	}
	if c := put(`,"studios":["lostfilm"]`); c != 200 {
		t.Fatalf("PUT studios: %d", c)
	}
	want := []string{"LostFilm"}
	if got := studios(); !reflect.DeepEqual(got, want) {
		t.Fatalf("studios %q, want %q", got, want)
	}
	if c := put(`,"remoteQuality":"720"`); c != 200 {
		t.Fatalf("PUT remoteQuality: %d", c)
	}
	if got := studios(); !reflect.DeepEqual(got, want) {
		t.Fatalf("a PUT without studios lost them: %q", got)
	}
	many := make([]string, 31)
	for i := range many {
		many[i] = `"S` + strings.Repeat("x", i+1) + `"`
	}
	if c := put(`,"studios":[` + strings.Join(many, ",") + `]`); c != 400 {
		t.Fatalf("31 studios: %d", c)
	}
	if got := studios(); !reflect.DeepEqual(got, want) {
		t.Fatalf("a refused PUT saved something: %q", got)
	}
}
