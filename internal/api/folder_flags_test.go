package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// the folder marks on the web API: «Только для взрослых» is judged 18+ (a
// kid loses the 0+ film everywhere, an adult keeps it), «Скрыть из общих
// подборок» keeps a film off everyone's home shelves but not out of the
// library, search or its own page
func TestFolderMarksOnTheWebAPI(t *testing.T) {
	e := newLibEnv(t)
	adult, _ := e.movie("Взрослый", "0+", "a")
	hidden, _ := e.movie("Скрытый", "0+", "b")
	plain, _ := e.movie("Обычный", "0+", "c")
	if _, err := e.db.Exec(`UPDATE items SET adult=1 WHERE id=?`, adult); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE items SET unlisted=1 WHERE id=?`, hidden); err != nil {
		t.Fatal(err)
	}
	kid := e.kid("12+")
	has := func(body, title string) bool { return strings.Contains(body, `"`+title+`"`) }

	// the library and search
	for _, path := range []string{"/api/items?kind=movie", "/api/search?q=" + "%D0%92%D0%B7%D1%80%D0%BE%D1%81%D0%BB%D1%8B%D0%B9"} {
		if w := e.do("GET", path, "", kid); w.Code != 200 || has(w.Body.String(), "Взрослый") {
			t.Fatalf("kid %s: %d %s", path, w.Code, w.Body)
		}
		if w := e.do("GET", path, "", e.admin); w.Code != 200 || !has(w.Body.String(), "Взрослый") {
			t.Fatalf("admin %s: %d %s", path, w.Code, w.Body)
		}
	}
	if w := e.do("GET", "/api/items/"+itoa(adult), "", kid); w.Code != 404 {
		t.Fatalf("kid opens it: %d", w.Code)
	}
	if w := e.do("GET", "/api/items/"+itoa(adult), "", e.admin); w.Code != 200 {
		t.Fatalf("admin cannot open it: %d", w.Code)
	}
	// the unlisted film: in the library, opened directly, off home
	if w := e.do("GET", "/api/items?kind=movie", "", kid); !has(w.Body.String(), "Скрытый") {
		t.Fatalf("unlisted is out of the library: %s", w.Body)
	}
	if w := e.do("GET", "/api/items/"+itoa(hidden), "", kid); w.Code != 200 {
		t.Fatalf("unlisted cannot be opened: %d", w.Code)
	}
	for _, c := range []struct {
		name   string
		cookie bool
	}{{"kid", true}, {"admin", false}} {
		ck := e.admin
		if c.cookie {
			ck = kid
		}
		w := e.do("GET", "/api/home", "", ck)
		if w.Code != 200 {
			t.Fatalf("%s home %d", c.name, w.Code)
		}
		var home struct {
			Shelves []struct {
				ID    string `json:"id"`
				Cards []Card `json:"cards"`
			} `json:"shelves"`
		}
		json.Unmarshal(w.Body.Bytes(), &home)
		found := false
		for _, s := range home.Shelves {
			if s.ID != "recent-movies" {
				continue
			}
			found = true
			ids := map[int64]bool{}
			for _, cd := range s.Cards {
				ids[cd.ID] = true
			}
			if ids[hidden] || !ids[plain] || (c.cookie && ids[adult]) || (!c.cookie && !ids[adult]) {
				t.Fatalf("%s recent movies %v", c.name, ids)
			}
		}
		if !found {
			t.Fatalf("%s: no recent-movies shelf", c.name)
		}
	}
	// the channel editor's options: what a kid may see
	if w := e.do("GET", "/api/channels/options", "", kid); w.Code == 200 && has(w.Body.String(), "Взрослый") {
		t.Fatalf("kid channel options: %s", w.Body)
	}
}
