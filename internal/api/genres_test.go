package api

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

// fakeGenres answers like TMDB's genre lists: names in the lookup language.
type fakeGenres struct{ asked []string }

func (f *fakeGenres) GenreNames(ctx context.Context, kind string) (map[int]string, error) {
	lang := tmdb.ContextLanguage(ctx)
	f.asked = append(f.asked, kind+"/"+lang)
	if lang == "en-US" {
		return map[int]string{18: "Drama", 80: "Crime"}, nil
	}
	return map[int]string{18: "драма", 80: "криминал"}, nil
}

// With TMDB ids, genres come from TMDB's list in the viewer's language.
func TestGenresByTMDBIdInTheViewersLanguage(t *testing.T) {
	g := &fakeGenres{}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Genres = g })
	id, _ := e.movie("Брат", "16+", "0123456789")
	if _, err := e.db.Exec(`UPDATE items SET meta_json='{"genres":["Drama","Crime"],"genreIds":[80,18]}' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	w := e.do("GET", "/api/items/"+itoa(id), "", e.admin)
	if !strings.Contains(w.Body.String(), `"genres":["криминал","драма"]`) {
		t.Fatalf("ru: %s", w.Body)
	}
	w = e.do("GET", "/api/items/"+itoa(id), "", e.admin, "Accept-Language", "en")
	if !strings.Contains(w.Body.String(), `"genres":["Crime","Drama"]`) || !strings.Contains(w.Body.String(), `"subtitle":"Crime · Drama"`) {
		t.Fatalf("en: %s", w.Body)
	}
	if len(g.asked) == 0 || g.asked[0] != "movie/ru-RU" {
		t.Fatalf("asked %v", g.asked)
	}
}
