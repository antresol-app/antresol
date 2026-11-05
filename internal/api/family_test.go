package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/library"
)

// profile creates a profile and signs it in.
func (e *libEnv) profile(userJSON string) (int64, *http.Cookie) {
	e.t.Helper()
	w := e.do("POST", "/api/users", userJSON, e.admin)
	if w.Code != 201 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	var u auth.User
	if err := json.Unmarshal(w.Body.Bytes(), &u); err != nil {
		e.t.Fatal(err)
	}
	c := session(e.do("POST", "/api/login", `{"userId":`+itoa(u.ID)+`}`, nil))
	if c == nil {
		e.t.Fatal("login")
	}
	return u.ID, c
}

// series adds a series of one season with n episodes; returns its id and the episodes'.
func (e *libEnv) series(title, rating string, n int) (int64, []int64) {
	e.t.Helper()
	var eps []int64
	for i := 1; i <= n; i++ {
		p := filepath.Join(e.media, title+" s01e0"+itoa(int64(i))+".mkv")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			e.t.Fatal(err)
		}
		id, err := e.repo.UpsertEpisode(context.Background(), library.EpisodeInput{SeriesTitle: title, SeriesRating: rating,
			Season: 1, Episode: i, File: library.FileInput{Path: p, Size: 1}})
		if err != nil {
			e.t.Fatal(err)
		}
		eps = append(eps, id)
	}
	ep, _ := e.repo.Item(context.Background(), eps[0])
	season, _ := e.repo.Item(context.Background(), ep.ParentID)
	return season.ParentID, eps
}

type familyOut struct {
	Members []FamilyMember `json:"members"`
}

func (e *libEnv) familyOf(id int64, c *http.Cookie) familyOut {
	e.t.Helper()
	w := e.do("GET", "/api/items/"+itoa(id)+"/family", "", c)
	if w.Code != 200 {
		e.t.Fatalf("family %d: %d %s", id, w.Code, w.Body)
	}
	return decodeJSON[familyOut](e.t, w.Body.Bytes())
}

func (e *libEnv) put(path, body string, c *http.Cookie) {
	e.t.Helper()
	if w := e.do("PUT", path, body, c); w.Code != 204 {
		e.t.Fatalf("PUT %s: %d %s", path, w.Code, w.Body)
	}
}

type famProgress struct {
	PositionMs int64   `json:"positionMs"`
	DurationMs int64   `json:"durationMs"`
	Finished   bool    `json:"finished"`
	Locator    string  `json:"locator"`
	Fraction   float64 `json:"fraction"`
	UpdatedAt  *string `json:"updatedAt"`
}

func (e *libEnv) myProgress(id int64, c *http.Cookie) famProgress {
	e.t.Helper()
	w := e.do("GET", "/api/progress/"+itoa(id), "", c)
	if w.Code != 200 {
		e.t.Fatalf("progress %d: %d %s", id, w.Code, w.Body)
	}
	return decodeJSON[famProgress](e.t, w.Body.Bytes())
}

// Every kind with progress keeps one position per profile, end to end:
// the save, the read back, «Продолжить» / «Читаю сейчас» and the watched mark.
func TestProgressIsPerProfile(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Брат", "16+", "0123456789")
	_, eps := e.series("Сёгун", "16+", 2)
	book := e.book("Мастер", ".epub")
	audio, _ := e.audiobook("Война", 2)
	_, vols := e.comic("Берсерк", "16+", 2)
	_, mom := e.profile(`{"name":"Мама"}`)

	e.put("/api/progress/"+itoa(movie), `{"positionMs":1000,"durationMs":4000}`, e.admin)
	e.put("/api/progress/"+itoa(eps[0]), `{"positionMs":1000,"durationMs":4000}`, e.admin)
	e.put("/api/progress/"+itoa(audio), `{"positionMs":60000,"durationMs":600000}`, e.admin)
	e.put("/api/progress/"+itoa(book), `{"locator":"epubcfi(/6/4!/4/2)","fraction":0.4}`, e.admin)
	e.put("/api/progress/"+itoa(vols[0].id), `{"locator":"page:2","fraction":0.66}`, e.admin)
	for _, id := range []int64{movie, eps[0], audio, book, vols[0].id} {
		if p := e.myProgress(id, mom); p.PositionMs != 0 || p.Locator != "" || p.UpdatedAt != nil {
			t.Fatalf("item %d: Мама sees Папа's progress %+v", id, p)
		}
		if p := e.myProgress(id, e.admin); p.UpdatedAt == nil {
			t.Fatalf("item %d: Папа's own progress lost", id)
		}
	}
	// Мама's shelves are hers: nothing to continue or being read
	w := e.do("GET", "/api/home", "", mom)
	var home struct {
		Shelves []shelf `json:"shelves"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &home); err != nil {
		t.Fatal(err)
	}
	for _, s := range home.Shelves {
		if (s.ID == "continue" || s.ID == "reading") && len(s.Cards) > 0 {
			t.Fatalf("Мама's %s: %+v", s.ID, s.Cards)
		}
	}
	if w := e.do("GET", "/api/books/home", "", mom); strings.Contains(w.Body.String(), `"reading"`) {
		t.Fatalf("Мама's books home: %s", w.Body)
	}
	// Мама's watched mark is hers alone
	if w := e.do("POST", "/api/items/"+itoa(movie)+"/watched", `{"watched":true}`, mom); w.Code != 204 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if p := e.myProgress(movie, e.admin); p.Finished || p.PositionMs != 1000 {
		t.Fatalf("Мама's mark changed Папа's progress: %+v", p)
	}
	if w := e.do("POST", "/api/items/"+itoa(movie)+"/watched", `{"watched":false}`, e.admin); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if p := e.myProgress(movie, mom); !p.Finished {
		t.Fatalf("Папа's unmark removed Мама's: %+v", p)
	}
}

func TestFamilyViewMovieAndBooks(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Брат", "16+", "0123456789")
	book := e.book("Мастер", ".epub")
	_, vols := e.comic("Берсерк", "16+", 1)
	momID, mom := e.profile(`{"name":"Мама","avatar":"grad-3"}`)

	if got := e.familyOf(movie, e.admin).Members; len(got) != 0 {
		t.Fatalf("nobody else has progress: %+v", got)
	}
	e.put("/api/progress/"+itoa(movie), `{"positionMs":2040000,"durationMs":6000000}`, mom)
	e.put("/api/progress/"+itoa(book), `{"locator":"epubcfi(/6/14!/4/2)","fraction":0.42}`, mom)
	e.put("/api/progress/"+itoa(vols[0].id), `{"locator":"page:2","fraction":0.66}`, mom)

	m := e.familyOf(movie, e.admin).Members
	if len(m) != 1 || m[0].UserID != momID || m[0].Name != "Мама" || m[0].Avatar != "grad-3" || m[0].ItemID != movie ||
		m[0].PositionMs != 2040000 || m[0].DurationMs != 6000000 || m[0].Finished || m[0].UpdatedAt == "" {
		t.Fatalf("movie: %+v", m)
	}
	if m := e.familyOf(book, e.admin).Members; len(m) != 1 || m[0].Locator != "epubcfi(/6/14!/4/2)" || m[0].Fraction != 0.42 {
		t.Fatalf("book: %+v", m)
	}
	if m := e.familyOf(vols[0].id, e.admin).Members; len(m) != 1 || m[0].Page != 2 || m[0].Pages != 3 || m[0].Volume != 1 {
		t.Fatalf("volume: %+v", m)
	}
	// the caller is never in their own family view
	if m := e.familyOf(movie, mom).Members; len(m) != 0 {
		t.Fatalf("Мама sees herself: %+v", m)
	}
	// an unknown item has none
	if w := e.do("GET", "/api/items/999999/family", "", e.admin); w.Code != 404 {
		t.Fatalf("missing item: %d", w.Code)
	}
}

func TestFamilyViewSeriesWhereEveryoneIs(t *testing.T) {
	e := newLibEnv(t)
	series, eps := e.series("Сёгун", "16+", 3)
	_, mom := e.profile(`{"name":"Мама"}`)
	_, son := e.profile(`{"name":"Петя"}`)
	_, gran := e.profile(`{"name":"Бабушка"}`)

	// Мама is in the middle of E2; Петя finished E1 (next: E2); Бабушка watched them all
	e.put("/api/progress/"+itoa(eps[0]), `{"positionMs":4000,"durationMs":4000}`, mom)
	e.put("/api/progress/"+itoa(eps[1]), `{"positionMs":1000,"durationMs":4000}`, mom)
	e.put("/api/progress/"+itoa(eps[0]), `{"positionMs":4000,"durationMs":4000}`, son)
	for _, ep := range eps {
		e.put("/api/progress/"+itoa(ep), `{"positionMs":4000,"durationMs":4000}`, gran)
	}
	by := map[string]FamilyMember{}
	for _, m := range e.familyOf(series, e.admin).Members {
		by[m.Name] = m
	}
	if m := by["Мама"]; m.ItemID != eps[1] || m.Season != 1 || m.Episode != 2 || m.PositionMs != 1000 || m.Next || m.Finished || m.Done != 1 || m.Total != 3 {
		t.Fatalf("Мама: %+v", m)
	}
	if m := by["Петя"]; m.ItemID != eps[1] || m.Episode != 2 || !m.Next || m.PositionMs != 0 || m.Done != 1 {
		t.Fatalf("Петя: %+v", m)
	}
	if m := by["Бабушка"]; !m.Finished || m.Done != 3 || m.Total != 3 {
		t.Fatalf("Бабушка: %+v", m)
	}
	// per episode: only who has a position in it
	if m := e.familyOf(eps[1], e.admin).Members; len(m) != 2 {
		t.Fatalf("episode 2: %+v", m)
	}
}

func TestFamilyVisibility(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Ёжик", "6+", "0123456789")
	_, mom := e.profile(`{"name":"Мама"}`)
	kidID, kid := e.profile(`{"name":"Вася","isKid":true,"maxRating":"12+"}`)
	_, kid2 := e.profile(`{"name":"Маша","isKid":true,"maxRating":"12+"}`)

	for _, c := range []*http.Cookie{e.admin, mom, kid, kid2} {
		e.put("/api/progress/"+itoa(movie), `{"positionMs":1000,"durationMs":4000}`, c)
	}
	names := func(c *http.Cookie) string {
		var out []string
		for _, m := range e.familyOf(movie, c).Members {
			out = append(out, m.Name)
		}
		return strings.Join(out, ",")
	}
	// adults see adults and kids; kids see nobody
	if got := names(mom); !strings.Contains(got, "Папа") || !strings.Contains(got, "Вася") || !strings.Contains(got, "Маша") {
		t.Fatalf("Мама sees %q", got)
	}
	if got := names(kid); got != "" {
		t.Fatalf("a kid sees %q", got)
	}
	// opting out hides one's own progress from everyone
	if w := e.do("PATCH", "/api/me", `{"hideProgress":true}`, mom); w.Code != 200 || !strings.Contains(w.Body.String(), `"hideProgress":true`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := names(e.admin); strings.Contains(got, "Мама") {
		t.Fatalf("hidden Мама still shown: %q", got)
	}
	// …but she still sees the others
	if got := names(mom); !strings.Contains(got, "Папа") {
		t.Fatalf("Мама sees %q", got)
	}
	// a kid's setting is the admin's
	if w := e.do("PATCH", "/api/me", `{"hideProgress":true}`, kid); w.Code != 403 {
		t.Fatalf("kid opt-out: %d", w.Code)
	}
	if w := e.do("PATCH", "/api/users/"+itoa(kidID), `{"hideProgress":true}`, e.admin); w.Code != 200 {
		t.Fatalf("admin: %d %s", w.Code, w.Body)
	}
	if got := names(e.admin); strings.Contains(got, "Вася") || !strings.Contains(got, "Маша") {
		t.Fatalf("after hiding Вася: %q", got)
	}
}

func TestAdoptWritesOnlyOwnProgress(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Брат", "16+", "0123456789")
	book := e.book("Мастер", ".epub")
	momID, mom := e.profile(`{"name":"Мама"}`)
	kidID, kid := e.profile(`{"name":"Вася","isKid":true,"maxRating":"18+"}`)

	e.put("/api/progress/"+itoa(movie), `{"positionMs":2000000,"durationMs":6000000}`, mom)
	e.put("/api/progress/"+itoa(book), `{"locator":"epubcfi(/6/14!/4/2)","fraction":0.42}`, mom)
	before := e.myProgress(movie, mom)

	w := e.do("POST", "/api/progress/"+itoa(movie)+"/adopt", `{"userId":`+itoa(momID)+`}`, e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if p := decodeJSON[famProgress](t, w.Body.Bytes()); p.PositionMs != 2000000 || p.DurationMs != 6000000 || p.UpdatedAt == nil {
		t.Fatalf("adopted: %+v", p)
	}
	if p := e.myProgress(movie, e.admin); p.PositionMs != 2000000 {
		t.Fatalf("Папа's own: %+v", p)
	}
	if p := e.myProgress(movie, mom); *p.UpdatedAt != *before.UpdatedAt || p.PositionMs != before.PositionMs {
		t.Fatalf("Мама's progress was written: %+v", p)
	}
	// the jump is not watching: no played time
	var ms int64
	if err := e.db.QueryRow(`SELECT COALESCE(SUM(ms),0) FROM watch_time WHERE user_id=1`).Scan(&ms); err != nil || ms != 0 {
		t.Fatalf("watch time %d %v", ms, err)
	}
	if w := e.do("POST", "/api/progress/"+itoa(book)+"/adopt", `{"userId":`+itoa(momID)+`}`, e.admin); w.Code != 200 {
		t.Fatalf("book: %d %s", w.Code, w.Body)
	}
	if p := e.myProgress(book, e.admin); p.Locator != "epubcfi(/6/14!/4/2)" || p.Fraction != 0.42 {
		t.Fatalf("book adopted: %+v", p)
	}
	// a kid cannot take an adult's place (nobody is in a kid's family view)
	if w := e.do("POST", "/api/progress/"+itoa(movie)+"/adopt", `{"userId":`+itoa(momID)+`}`, kid); w.Code != 404 {
		t.Fatalf("kid adopt: %d", w.Code)
	}
	// nor anyone a hidden profile's
	if w := e.do("PATCH", "/api/me", `{"hideProgress":true}`, mom); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := e.do("POST", "/api/progress/"+itoa(movie)+"/adopt", `{"userId":`+itoa(momID)+`}`, e.admin); w.Code != 404 {
		t.Fatalf("hidden adopt: %d", w.Code)
	}
	// no position to take
	if w := e.do("POST", "/api/progress/"+itoa(movie)+"/adopt", `{"userId":`+itoa(kidID)+`}`, e.admin); w.Code != 404 {
		t.Fatalf("no position: %d", w.Code)
	}
	if w := e.do("POST", "/api/progress/"+itoa(movie)+"/adopt", `{"userId":1}`, e.admin); w.Code != 404 {
		t.Fatalf("self: %d", w.Code)
	}
}

func TestFamilyRespectsRatingLimit(t *testing.T) {
	e := newLibEnv(t)
	adult, _ := e.movie("Брат", "18+", "0123456789")
	_, mom := e.profile(`{"name":"Мама"}`)
	_, teen := e.profile(`{"name":"Вася","isKid":true,"maxRating":"12+"}`)
	e.put("/api/progress/"+itoa(adult), `{"positionMs":1000,"durationMs":4000}`, mom)
	if w := e.do("GET", "/api/items/"+itoa(adult)+"/family", "", teen); w.Code != 404 {
		t.Fatalf("hidden title's family view: %d", w.Code)
	}
}

func TestHomeFamilyShelf(t *testing.T) {
	e := newLibEnv(t)
	movie, _ := e.movie("Брат", "16+", "0123456789")
	adult, _ := e.movie("Ночь", "18+", "0123456789")
	mine, _ := e.movie("Мой", "16+", "0123456789")
	series, eps := e.series("Сёгун", "12+", 2)
	_, mom := e.profile(`{"name":"Мама"}`)
	_, kid := e.profile(`{"name":"Вася","isKid":true,"maxRating":"16+"}`)

	e.put("/api/progress/"+itoa(movie), `{"positionMs":1000,"durationMs":4000}`, mom)
	e.put("/api/progress/"+itoa(adult), `{"positionMs":1000,"durationMs":4000}`, mom)
	e.put("/api/progress/"+itoa(mine), `{"positionMs":1000,"durationMs":4000}`, mom)
	e.put("/api/progress/"+itoa(mine), `{"positionMs":2000,"durationMs":4000}`, e.admin) // in my «Продолжить»
	e.put("/api/progress/"+itoa(eps[1]), `{"positionMs":1000,"durationMs":4000}`, kid)
	e.put("/api/progress/"+itoa(eps[0]), `{"positionMs":1000,"durationMs":4000}`, mom)

	shelfOf := func(c *http.Cookie, q string) *shelf {
		w := e.do("GET", "/api/home"+q, "", c)
		var home struct {
			Shelves []shelf `json:"shelves"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &home); err != nil {
			t.Fatal(err)
		}
		for i := range home.Shelves {
			if home.Shelves[i].ID == "family" {
				return &home.Shelves[i]
			}
		}
		return nil
	}
	if shelfOf(e.admin, "") != nil {
		t.Fatal("plain /api/home (the app) has no family shelf")
	}
	s := shelfOf(e.admin, "?family=1")
	if s == nil || s.Title != "Семья смотрит" {
		t.Fatalf("%+v", s)
	}
	var ids []int64
	for _, c := range s.Cards {
		ids = append(ids, c.ID)
	}
	if len(ids) != 3 || ids[0] != series || !strings.HasPrefix(s.Cards[0].Subtitle, "Мама, Вася · С1 Э") {
		t.Fatalf("cards %+v", s.Cards)
	}
	for _, c := range s.Cards {
		if c.ID == mine {
			t.Fatal("a title of my own «Продолжить» is left out")
		}
	}
	// a title over a profile's rating limit is never on its shelf
	_, aunt := e.profile(`{"name":"Тётя","maxRating":"16+"}`)
	for _, c := range shelfOf(aunt, "?family=1").Cards {
		if c.ID == adult {
			t.Fatal("an 18+ title on a 16+ profile's shelf")
		}
	}
	// a kid gets an empty shelf
	if s := shelfOf(kid, "?family=1"); s == nil || len(s.Cards) != 0 {
		t.Fatalf("kid: %+v", s)
	}
}
