package api

import (
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
)

// me.libraryOnly: VAULT_LIBRARY_ONLY, false (and present) otherwise.
func TestMeLibraryOnly(t *testing.T) {
	off := newEnv(t)
	want := `"libraryOnly":false`
	if libraryEdition {
		want = `"libraryOnly":true` // always, in the core build
	}
	if w := off.do("GET", "/api/me", "", off.setup()); !strings.Contains(w.Body.String(), want) {
		t.Fatalf("%s", w.Body)
	}
	on := newEnv(t)
	on.h = NewRouter(Deps{Users: on.store, Sessions: auth.NewSessions(on.db), LibraryOnly: true})
	if w := on.do("GET", "/api/me", "", on.setup()); !strings.Contains(w.Body.String(), `"libraryOnly":true`) {
		t.Fatalf("%s", w.Body)
	}
}
