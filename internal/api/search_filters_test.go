package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestSearchKidRatingAndGenreFilter(t *testing.T) {
	e := newLibEnv(t)
	e.movie("Шрек", "6+", "a")
	e.movie("Шрам", "18+", "b")
	kid := e.kid("6+")
	body := func(c *http.Cookie, q string) string { return e.do("GET", "/api/search?q="+q, "", c).Body.String() }
	if b := body(kid, "шр"); !strings.Contains(b, "Шрек") || strings.Contains(b, "Шрам") {
		t.Fatalf("kid: %s", b)
	}
	if b := body(e.admin, "шр"); !strings.Contains(b, "Шрам") {
		t.Fatalf("admin: %s", b)
	}
	if b := body(e.admin, ""); !strings.Contains(b, `"library":[]`) {
		t.Fatalf("empty: %s", b)
	}
	e.album("Кино", "Группа", 1990, tTrack{1, 1, "Песня", "1.flac", ""})
	if w := e.do("GET", "/api/music/albums?genre=Nope", "", e.admin); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/music/albums?sort=bogus", "", e.admin); w.Code != 400 {
		t.Fatalf("%d", w.Code)
	}
}
