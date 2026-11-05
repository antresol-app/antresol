package api

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/music/musictest"
)

func (e *libEnv) shuffleLib() musictest.Shuffle {
	e.t.Helper()
	return musictest.ShuffleLib(e.t, e.repo, e.db, filepath.Join(e.media, "music"))
}

func (e *libEnv) mustExec(q string, args ...any) {
	e.t.Helper()
	musictest.Exec(e.t, e.db, q, args...)
}

type shuffleOut struct {
	Tracks []TrackDTO `json:"tracks"`
}

// shuffleIDs asks for a shuffle and checks a 200 with distinct tracks.
func (e *libEnv) shuffleIDs(query string, c *http.Cookie) []int64 {
	e.t.Helper()
	w := e.do("GET", "/api/music/shuffle?"+query, "", c)
	if w.Code != 200 {
		e.t.Fatalf("%s: %d %s", query, w.Code, w.Body)
	}
	out := decodeJSON[shuffleOut](e.t, w.Body.Bytes())
	seen := map[int64]bool{}
	var ids []int64
	for _, tr := range out.Tracks {
		if seen[tr.ID] {
			e.t.Fatalf("%s: %d twice", query, tr.ID)
		}
		seen[tr.ID] = true
		ids = append(ids, tr.ID)
	}
	return ids
}

func TestShuffleEndpoint(t *testing.T) {
	e := newLibEnv(t)
	f := e.shuffleLib()
	if got := e.shuffleIDs("source=all", e.admin); !musictest.SameSet(got, musictest.Set(f.First, f.Comp, f.Adult, f.NoArtist)) {
		t.Fatalf("all %v", got)
	}
	if got := e.shuffleIDs("source=genre&genre=%D1%80%D0%BE%D0%BA", e.admin); !musictest.SameSet(got, musictest.Set(f.First, f.Adult, f.NoArtist)) { // «рок»
		t.Fatalf("genre %v", got)
	}
	if got := e.shuffleIDs("source=artist&artist="+itoa(f.TestArtist), e.admin); !musictest.SameSet(got, musictest.Set(f.First, []int64{f.CompTest})) {
		t.Fatalf("artist %v", got)
	}
	for _, c := range []struct {
		q, want string
		code    int
	}{
		{"source=genre&genre=%D0%94%D0%B6%D0%B0%D0%B7", "Не найдено", 404}, // «Джаз»
		{"source=genre", "Не найдено", 404},
		{"source=genre&genre=+", "Не найдено", 404},
		{"source=bogus", "Неизвестный источник", 400},
		{"", "Неизвестный источник", 400},
		{"source=artist&artist=x", "Неверный id", 400},
		{"source=artist", "Неверный id", 400},
		{"source=artist&artist=" + itoa(f.FirstAlbum), "Не найдено", 404},
		{"source=artist&artist=999999", "Не найдено", 404},
	} {
		w := e.do("GET", "/api/music/shuffle?"+c.q, "", e.admin)
		if w.Code != c.code || strings.TrimSpace(w.Body.String()) != `{"error":"`+c.want+`"}` {
			t.Fatalf("%q: %d %s", c.q, w.Code, w.Body)
		}
	}
	// an artist without eligible tracks
	empty := musictest.Artist(t, e.db, "Пустой")
	if w := e.do("GET", "/api/music/shuffle?source=artist&artist="+itoa(empty), "", e.admin); w.Code != 404 || !strings.Contains(w.Body.String(), "Не найдено") {
		t.Fatalf("empty artist %d %s", w.Code, w.Body)
	}
	// a compilation-only artist: their compilation track
	guest := musictest.Artist(t, e.db, "Гость")
	e.mustExec(`UPDATE tracks SET artist='Гость' WHERE item_id=?`, f.CompTest)
	if got := e.shuffleIDs("source=artist&artist="+itoa(guest), e.admin); len(got) != 1 || got[0] != f.CompTest {
		t.Fatalf("compilation-only artist %v", got)
	}
	// a 1-track genre (added here only: the shared counts stay)
	_, one := e.genreAlbum("Соло", "Один", "Джаз-рок", "Соло")
	if got := e.shuffleIDs("source=genre&genre="+url.QueryEscape("джаз-рок"), e.admin); len(got) != 1 || got[0] != one[0] {
		t.Fatalf("one-track genre %v", got)
	}
}

func TestShuffleKidNeverHearsHidden(t *testing.T) {
	e := newLibEnv(t)
	f := e.shuffleLib()
	kid := e.kid("")
	hidden := musictest.Set(f.Adult, f.NoArtist, f.Missing)
	sawComp := false
	for i := 0; i < 50; i++ {
		for _, q := range []string{"source=all", "source=genre&genre=%D0%A0%D0%BE%D0%BA"} {
			for _, id := range e.shuffleIDs(q, kid) {
				if hidden[id] {
					t.Fatalf("kid %s got hidden %d", q, id)
				}
				sawComp = sawComp || (q == "source=all" && id == f.CompOther)
			}
		}
	}
	if !sawComp {
		t.Fatal("the compilation track by «Другой артист» belongs to the kid's «все»")
	}
	w := e.do("GET", "/api/music/shuffle?source=artist&artist="+itoa(f.OtherArtist), "", kid)
	if w.Code != 404 || strings.TrimSpace(w.Body.String()) != `{"error":"Не найдено"}` {
		t.Fatalf("kid hidden artist %d %s", w.Code, w.Body)
	}
	if got := e.shuffleIDs("source=artist&artist="+itoa(f.OtherArtist), e.admin); !musictest.SameSet(got, musictest.Set(f.Adult, []int64{f.CompOther})) {
		t.Fatalf("admin other artist %v", got)
	}
}

func TestGenresEndpoint(t *testing.T) {
	e := newLibEnv(t)
	e.shuffleLib()
	type genre struct {
		Name   string `json:"name"`
		Albums int    `json:"albums"`
		Songs  int    `json:"songs"`
	}
	type out struct {
		Genres []genre `json:"genres"`
	}
	for _, c := range []struct {
		who  *http.Cookie
		want []genre
	}{
		{e.admin, []genre{{"Рок", 3, 6}, {"Поп", 1, 2}}},
		{e.kid(""), []genre{{"Рок", 1, 3}, {"Поп", 1, 2}}},
	} {
		w := e.do("GET", "/api/music/genres", "", c.who)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		got := decodeJSON[out](t, w.Body.Bytes()).Genres
		if len(got) != len(c.want) {
			t.Fatalf("%s", w.Body)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s", w.Body)
			}
		}
	}
	// songs desc, then name
	e.genreAlbum("Соло", "Один", "Блюз", "Соло", "Соло")
	got := decodeJSON[out](t, e.do("GET", "/api/music/genres", "", e.admin).Body.Bytes()).Genres
	if len(got) != 3 || got[1].Name != "Блюз" || got[2].Name != "Поп" {
		t.Fatalf("tie order %+v", got)
	}
}

func (e *libEnv) genreAlbum(albumArtist, title, genre string, artists ...string) (int64, []int64) {
	e.t.Helper()
	return musictest.GenreAlbum(e.t, e.repo, filepath.Join(e.media, "music"), albumArtist, title, genre, artists...)
}
