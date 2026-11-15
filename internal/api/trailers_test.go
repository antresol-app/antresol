package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/antresol-app/antresol/internal/metadata/tmdb"
)

type fakeTrailers struct {
	asked []string
	vs    []tmdb.Video
}

func (f *fakeTrailers) Videos(_ context.Context, kind string, id int) ([]tmdb.Video, bool) {
	f.asked = append(f.asked, kind+"/"+itoa(int64(id)))
	return f.vs, f.vs != nil
}

func (e *libEnv) trailersOf(id int64, hdr ...string) []Trailer {
	e.t.Helper()
	w := e.do("GET", "/api/items/"+itoa(id)+"/trailers", "", e.admin, hdr...)
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		Trailers []Trailer `json:"trailers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || out.Trailers == nil {
		e.t.Fatalf("%v %s", err, w.Body)
	}
	return out.Trailers
}

func TestTrailersOrderAndSeries(t *testing.T) {
	f := &fakeTrailers{vs: []tmdb.Video{
		{Name: "de", Site: "YouTube", Key: "de1", Type: "Trailer", Lang: "de", Official: true},
		{Name: "en teaser", Site: "YouTube", Key: "en2", Type: "Teaser", Lang: "en", Official: true},
		{Name: "en fan", Site: "YouTube", Key: "en3", Type: "Trailer", Lang: "en"},
		{Name: "en", Site: "YouTube", Key: "en1", Type: "Trailer", Lang: "en", Official: true},
		{Name: "ru teaser", Site: "YouTube", Key: "ru2", Type: "Teaser", Lang: "ru", Official: true},
		{Name: "ru", Site: "YouTube", Key: "ru1", Type: "Trailer", Lang: "ru", Official: true},
		{Name: "vimeo", Site: "Vimeo", Key: "v", Type: "Trailer", Lang: "ru", Official: true},
	}}
	e := newLibEnvWith(t, func(d *Deps, _ *sql.DB) { d.Trailers = f })
	movie, _ := e.movie("Фильм", "", "0123456789abcdef")
	series, eps := e.series("Сериал", "", 1)
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=603 WHERE id=?`, movie); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=1399 WHERE id=?`, series); err != nil {
		t.Fatal(err)
	}
	keys := func(ts []Trailer) (out []string) {
		for _, t := range ts {
			out = append(out, t.Key)
		}
		return out
	}
	got := e.trailersOf(movie)
	if want := "[ru1 ru2 en1 en2 en3 de1]"; fmt.Sprint(keys(got)) != want {
		t.Fatalf("ru order %v, want %s", keys(got), want)
	}
	if got[0].URL != "https://www.youtube.com/watch?v=ru1" || got[0].Site != "YouTube" || got[0].Type != "Trailer" || got[0].Lang != "ru" || !got[0].Official {
		t.Fatalf("%+v", got[0])
	}
	if got := e.trailersOf(movie, "Accept-Language", "en"); fmt.Sprint(keys(got)) != "[en1 en2 en3 de1 ru1 ru2]" {
		t.Fatalf("en order %v", keys(got))
	}
	// an episode answers with its series' trailers
	e.trailersOf(eps[0])
	if last := f.asked[len(f.asked)-1]; last != "tv/1399" {
		t.Fatalf("episode asked %s", last)
	}
	// no TMDB id: an empty list, TMDB not asked
	n := len(f.asked)
	other, _ := e.movie("Без TMDB", "", "fedcba9876543210")
	if got := e.trailersOf(other); len(got) != 0 || len(f.asked) != n {
		t.Fatalf("%v %v", got, f.asked)
	}
}

func TestTrailersWithoutTMDB(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Фильм", "", "0123456789abcdef")
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=603 WHERE id=?`, movie); err != nil {
		t.Fatal(err)
	}
	if got := e.trailersOf(movie); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}
