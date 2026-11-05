package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
	"github.com/antresol-app/antresol/internal/music"
	"github.com/antresol-app/antresol/internal/progress"
)

// fakeHLS records Start calls instead of running ffmpeg.
type fakeHLS struct {
	started []media.Decision
	opts    []media.StartOptions
	err     error // returned by Start when set
	gone    bool  // Touch reports the session gone
	stopped []string
	plErr   error // returned by Playlist when set
	segErr  error // returned by Segment when set
	dir     string
	// complete: the complete VOD playlist CompletePlaylist hands out ("" = none)
	complete string
}

func (f *fakeHLS) Start(_ context.Context, o media.StartOptions) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.started = append(f.started, o.Decision)
	f.opts = append(f.opts, o)
	return "sess1", nil
}
func (f *fakeHLS) Playlist(id string) (string, error) {
	if f.plErr != nil {
		return "", f.plErr
	}
	if id != "sess1" {
		return "", media.ErrNoSession
	}
	p := filepath.Join(f.dir, "index.m3u8")
	return p, os.WriteFile(p, []byte("#EXTM3U\n"), 0o644)
}
func (f *fakeHLS) CompletePlaylist(id string) (string, bool) {
	if id != "sess1" || f.complete == "" {
		return "", false
	}
	return f.complete, true
}
func (f *fakeHLS) Segment(id, name string) (string, error) {
	if f.segErr != nil {
		return "", f.segErr
	}
	return "", media.ErrNoSegment
}
func (f *fakeHLS) Touch(id string) (media.SessionStatus, error) {
	if id != "sess1" || f.gone {
		return media.SessionStatus{}, media.ErrNoSession
	}
	return media.SessionStatus{Segments: 3, Running: true}, nil
}
func (f *fakeHLS) Stop(id string) { f.stopped = append(f.stopped, id) }

func (f *fakeHLS) IOBusy() bool { return false }

type libEnv struct {
	*env
	repo  *library.Repo
	prog  *progress.Store
	hls   *fakeHLS
	media string
	admin *http.Cookie
}

func newLibEnv(t *testing.T) *libEnv { return newLibEnvWith(t, nil) }

// newLibEnvWith lets a test adjust the router's Deps.
func newLibEnvWith(t *testing.T, opt func(*Deps, *sql.DB)) *libEnv {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	st, ss := auth.NewStore(d), auth.NewSessions(d)
	repo, prog := library.NewRepo(d), progress.New(d)
	h := &fakeHLS{dir: t.TempDir()}
	e := &libEnv{env: &env{db: d, t: t, store: st}, repo: repo, prog: prog, hls: h, media: t.TempDir()}
	deps := Deps{Users: st, Sessions: ss, Library: repo, Progress: prog, HLS: h, DataDir: t.TempDir(),
		Music: music.New(d), ComicsCache: t.TempDir(),
		Probe: func(context.Context, string) (media.ProbeResult, error) {
			t.Fatal("probe must come from probe_json in tests")
			return media.ProbeResult{}, nil
		}}
	if opt != nil {
		opt(&deps, d)
	}
	e.h = NewRouter(deps)
	e.admin = e.setup()
	return e
}

// movie creates a movie with a real file of the given content on disk.
func (e *libEnv) movie(title, rating, content string) (itemID, fileID int64) {
	p := filepath.Join(e.media, title+".mkv")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: title, Year: 2000, Rating: rating,
		Backdrop: "file:" + filepath.Join(e.media, "fanart.jpg"), Poster: "0123456789abcdef0123456789abcdef01234567.jpg",
		File: library.FileInput{Path: p, Size: int64(len(content))}})
	if err != nil {
		e.t.Fatal(err)
	}
	fs, err := e.repo.FilesOf(context.Background(), id)
	if err != nil || len(fs) != 1 {
		e.t.Fatal(err, fs)
	}
	return id, fs[0].ID
}

// kid creates a kid profile ("" = no explicit maxRating) and logs it in.
func (e *libEnv) kid(maxRating string) *http.Cookie {
	body := `{"name":"Вася","isKid":true}`
	if maxRating != "" {
		body = `{"name":"Вася","isKid":true,"maxRating":"` + maxRating + `"}`
	}
	return e.login(body)
}

func (e *libEnv) login(userJSON string) *http.Cookie {
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
	return c
}

func TestHomeShelvesShape(t *testing.T) {
	e := newLibEnv(t)
	id, _ := e.movie("Брат", "16+", "0123456789abcdef")
	if err := e.prog.AddToWatchlist(context.Background(), 1, id); err != nil {
		t.Fatal(err)
	}
	if err := e.prog.Save(context.Background(), 1, id, 1000, 4000); err != nil {
		t.Fatal(err)
	}
	w := e.do("GET", "/api/home", "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var out struct {
		Hero    []Card `json:"hero"`
		Shelves []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Cards []Card `json:"cards"`
		} `json:"shelves"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	want := []string{"continue", "recently-played", "reading", "recent-movies", "recent-series", "watchlist"}
	if len(out.Shelves) != len(want) {
		t.Fatalf("%s", w.Body)
	}
	for i, s := range out.Shelves {
		if s.ID != want[i] || s.Title == "" || s.Cards == nil {
			t.Fatalf("shelf %d: %+v", i, s)
		}
	}
	if len(out.Hero) != 1 || out.Hero[0].ID != id {
		t.Fatalf("hero %+v", out.Hero)
	}
	c := out.Shelves[0].Cards
	if len(c) != 1 || c[0].ID != id || c[0].Progress != 0.25 || !c[0].OnServer {
		t.Fatalf("continue %+v", c)
	}
	m := out.Shelves[3].Cards[0]
	if m.Poster != "/api/images/0123456789abcdef0123456789abcdef01234567.jpg" ||
		m.Backdrop != "/api/images/local/"+itoa(id)+"/backdrop" || strings.Contains(w.Body.String(), e.media) {
		t.Fatalf("image urls must be opaque: %+v", m)
	}
	if len(out.Shelves[5].Cards) != 1 {
		t.Fatalf("watchlist %+v", out.Shelves[5])
	}
}

func TestKidFiltersRating(t *testing.T) {
	e := newLibEnv(t)
	adult, adultFile := e.movie("Брат", "18+", "0123456789")
	_, _ = e.movie("Бременские", "0+", "0123456789")
	kid := e.kid("12+")

	w := e.do("GET", "/api/items?kind=movie", "", kid)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Брат") || !strings.Contains(w.Body.String(), "Бременские") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/search?q=брат", "", kid); w.Code != 200 || strings.Contains(w.Body.String(), "Брат") {
		t.Fatalf("search %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/search?q=брат", "", e.admin); !strings.Contains(w.Body.String(), "Брат") {
		t.Fatalf("admin search %s", w.Body)
	}
	for _, p := range []string{"/api/items/" + itoa(adult), "/api/stream/" + itoa(adultFile), "/api/download/" + itoa(adultFile)} {
		if w := e.do("GET", p, "", kid); w.Code != 404 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	if w := e.do("POST", "/api/play/"+itoa(adultFile), `{}`, kid); w.Code != 404 {
		t.Fatalf("play: %d", w.Code)
	}
}

func TestKidEpisodesInheritSeriesRating(t *testing.T) {
	e := newLibEnv(t)
	p := filepath.Join(e.media, "s01e01.mkv")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ep, err := e.repo.UpsertEpisode(context.Background(), library.EpisodeInput{SeriesTitle: "Сёгун", SeriesRating: "18+",
		Season: 1, Episode: 1, File: library.FileInput{Path: p, Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	fs, err := e.repo.FilesOf(context.Background(), ep)
	if err != nil || len(fs) != 1 {
		t.Fatal(err, fs)
	}
	kid := e.kid("12+")
	for _, u := range []string{"/api/items/" + itoa(ep), "/api/stream/" + itoa(fs[0].ID)} {
		if w := e.do("GET", u, "", kid); w.Code != 404 {
			t.Fatalf("%s: %d", u, w.Code)
		}
		if w := e.do("GET", u, "", e.admin); w.Code != 200 {
			t.Fatalf("admin %s: %d %s", u, w.Code, w.Body)
		}
	}
}

func TestItemDetailSeries(t *testing.T) {
	e := newLibEnv(t)
	var epID int64
	for i := 1; i <= 2; i++ {
		p := filepath.Join(e.media, "s01e0"+itoa(int64(i))+".mkv")
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		id, err := e.repo.UpsertEpisode(context.Background(), library.EpisodeInput{SeriesTitle: "Сёгун", SeriesRating: "18+",
			Season: 1, Episode: i, File: library.FileInput{Path: p, Size: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 {
			epID = id
		}
	}
	ep, err := e.repo.Item(context.Background(), epID)
	if err != nil {
		t.Fatal(err)
	}
	season, err := e.repo.Item(context.Background(), ep.ParentID)
	if err != nil {
		t.Fatal(err)
	}
	w := e.do("GET", "/api/items/"+itoa(season.ParentID), "", e.admin)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var d ItemDetail
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Kind != "series" || len(d.Seasons) != 1 || len(d.Seasons[0].Episodes) != 2 || d.Seasons[0].Episodes[0].EpisodeNo != 1 ||
		!d.Actions.Play || d.Actions.ToServer {
		t.Fatalf("%s", w.Body)
	}
}

func TestKidWithoutMaxRatingDefaults12(t *testing.T) {
	e := newLibEnv(t)
	e.movie("Брат", "16+", "x")
	e.movie("Ёжик", "12+", "x")
	kid := e.kid("")
	w := e.do("GET", "/api/items", "", kid)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Брат") || !strings.Contains(w.Body.String(), "Ёжик") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := ratingLimit(auth.User{IsKid: true}); got != "12+" {
		t.Fatal(got)
	}
	if got := ratingLimit(auth.User{}); got != "" {
		t.Fatal(got)
	}
}

func TestImageURLDropsRemote(t *testing.T) {
	if u := imageURL(1, "poster", "https://image.tmdb.org/t/p/w500/x.jpg"); u != "" {
		t.Fatal(u)
	}
	if u := imageURL(1, "poster", "../etc/passwd"); u != "" {
		t.Fatal(u)
	}
}

// Music and books carry no age rating: a kid streams an unrated track, but a
// volume of an 18+ comic stays hidden (it inherits the series rating).
func TestKidAPIUnratedTrackStream(t *testing.T) {
	e := newLibEnv(t)
	ins := func(q string, args ...any) int64 {
		res, err := e.db.Exec(q, args...)
		if err != nil {
			e.t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	file := func(itemID int64, name string) int64 {
		p := filepath.Join(e.media, name)
		if err := os.WriteFile(p, []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
		return ins(`INSERT INTO files(item_id,path,size) VALUES(?,?,10)`, itemID, p)
	}
	artist := ins(`INSERT INTO items(kind,title) VALUES('artist','Кино')`)
	album := ins(`INSERT INTO items(kind,parent_id,title) VALUES('album',?,'Группа крови')`, artist)
	track := ins(`INSERT INTO items(kind,parent_id,title) VALUES('track',?,'Кукушка')`, album)
	trackFile := file(track, "01.flac")
	ins(`INSERT INTO tracks(item_id,file_id) VALUES(?,?)`, track, trackFile)
	comic := ins(`INSERT INTO items(kind,title,rating) VALUES('comic','Берсерк','18+')`)
	vol := ins(`INSERT INTO items(kind,parent_id,title) VALUES('comic_volume',?,'Том 1')`, comic)
	volFile := file(vol, "v01.cbz")

	kid := e.kid("12+")
	if w := e.do("GET", "/api/stream/"+itoa(trackFile), "", kid); w.Code != 200 {
		t.Fatalf("track: %d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/items/"+itoa(album), "", kid); w.Code == 404 {
		t.Fatalf("album hidden from kid")
	}
	if w := e.do("GET", "/api/stream/"+itoa(volFile), "", kid); w.Code != 404 {
		t.Fatalf("18+ volume: %d", w.Code)
	}
	if w := e.do("GET", "/api/stream/"+itoa(volFile), "", e.admin); w.Code != 200 {
		t.Fatalf("admin volume: %d", w.Code)
	}
}

func TestHomeHasListeningAndReading(t *testing.T) {
	e := newLibEnv(t)
	album, tr := e.simpleAlbum("Кино", "Группа крови", 1)
	b := e.book("Мастер и Маргарита", ".epub")
	e.do("POST", "/api/music/plays", `{"trackId":`+itoa(tr[0])+`}`, e.admin)
	e.do("PUT", "/api/progress/"+itoa(b), `{"locator":"epubcfi(/6/2)","fraction":0.2}`, e.admin)
	out := decodeJSON[shelvesOut](t, e.do("GET", "/api/home", "", e.admin).Body.Bytes())
	if out.Shelves[1].ID != "recently-played" || out.Shelves[1].Title != "Слушали недавно" || len(out.Shelves[1].Cards) != 1 || out.Shelves[1].Cards[0].ID != album {
		t.Fatalf("%+v", out.Shelves[1])
	}
	if out.Shelves[2].ID != "reading" || out.Shelves[2].Title != "Читаю сейчас" || len(out.Shelves[2].Cards) != 1 || out.Shelves[2].Cards[0].ID != b {
		t.Fatalf("%+v", out.Shelves[2])
	}
}

func TestContinueWatchingVideoOnly(t *testing.T) {
	e := newLibEnv(t)
	m, _ := e.movie("Брат", "16+", "0123456789")
	b := e.book("Мастер и Маргарита", ".epub")
	e.do("PUT", "/api/progress/"+itoa(m), `{"positionMs":1000,"durationMs":4000}`, e.admin)
	e.do("PUT", "/api/progress/"+itoa(b), `{"locator":"epubcfi(/6/2)","fraction":0.2}`, e.admin)
	out := decodeJSON[shelvesOut](t, e.do("GET", "/api/home", "", e.admin).Body.Bytes())
	if c := out.Shelves[0].Cards; out.Shelves[0].ID != "continue" || len(c) != 1 || c[0].ID != m {
		t.Fatalf("%+v", out.Shelves[0])
	}
}

func TestSearchSections(t *testing.T) {
	e := newLibEnv(t)
	e.movie("Кино про кино", "16+", "0123456789")
	album, tr := e.album("Кино", "Кино", 1990, tTrack{1, 1, "Кончится лето", "1.flac", ""})
	e.book("Кинопроба", ".epub")
	w := e.do("GET", "/api/search?q=кино", "", e.admin)
	out := decodeJSON[struct {
		Library    []Card     `json:"library"`
		Artists    []Card     `json:"artists"`
		Albums     []Card     `json:"albums"`
		Songs      []TrackDTO `json:"songs"`
		Books      []Card     `json:"books"`
		Audiobooks []Card     `json:"audiobooks"`
		Comics     []Card     `json:"comics"`
	}](t, w.Body.Bytes())
	if len(out.Library) != 1 || len(out.Artists) != 1 || out.Artists[0].Kind != "artist" || len(out.Albums) != 1 || out.Albums[0].ID != album ||
		len(out.Books) != 1 || out.Audiobooks == nil || out.Comics == nil {
		t.Fatalf("%s", w.Body)
	}
	w = e.do("GET", "/api/search?q=кончится", "", e.admin)
	if !strings.Contains(w.Body.String(), `"fileId":`) || !strings.Contains(w.Body.String(), `"id":`+itoa(tr[0])) {
		t.Fatalf("songs %s", w.Body)
	}
}

func TestStatsCountsMedia(t *testing.T) {
	e := newLibEnvWith(t, func(d *Deps, db *sql.DB) { d.DB = db })
	e.simpleAlbum("Кино", "Группа крови", 2)
	e.book("Мастер и Маргарита", ".epub")
	e.audiobook("Война и мир", 1)
	e.comic("Берсерк", "", 3)
	w := e.do("GET", "/api/stats", "", e.admin)
	out := decodeJSON[struct {
		Counts map[string]int `json:"counts"`
	}](t, w.Body.Bytes())
	want := map[string]int{"albums": 1, "tracks": 2, "books": 1, "audiobooks": 1, "comics": 1, "movies": 0}
	for k, v := range want {
		if n, ok := out.Counts[k]; !ok || n != v {
			t.Fatalf("%s: %d (%s)", k, n, w.Body)
		}
	}
}

// «Смотреть онлайн» on a library movie page needs its TMDB id.
func TestItemDetailMovieTmdbID(t *testing.T) {
	e := newLibEnv(t)
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "Дюна", Year: 2024, TmdbID: 438631,
		File: library.FileInput{Path: filepath.Join(e.media, "d.mkv"), Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	w := e.do("GET", "/api/items/"+itoa(id), "", e.admin)
	var out struct {
		TmdbID int64 `json:"tmdbId"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.TmdbID != 438631 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// The cast of a library item carries the server's URL of each actor's photo.
func TestItemDetailCastPhoto(t *testing.T) {
	e := newLibEnv(t)
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "Дюна", Year: 2024, TmdbID: 438631,
		File: library.FileInput{Path: filepath.Join(e.media, "d.mkv"), Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Exec(`UPDATE items SET meta_json=? WHERE id=?`,
		`{"cast":[{"name":"Тимоти Шаламе","character":"Пол","profile":"/abc.jpg"},{"name":"Без фото"}]}`, id); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Cast []CastMember `json:"cast"`
	}
	w := e.do("GET", "/api/items/"+itoa(id), "", e.admin)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	want := []CastMember{{"Тимоти Шаламе", "Пол", "/api/images/cast/abc.jpg"}, {"Без фото", "", ""}}
	if len(out.Cast) != 2 || out.Cast[0] != want[0] || out.Cast[1] != want[1] {
		t.Fatalf("cast %+v", out.Cast)
	}
}

// TMDB's role names are not translated: one in another script than the
// interface's is left out (the actor's name stays).
func TestRoleIn(t *testing.T) {
	for _, c := range []struct{ role, lang, want string }{
		{"Лёша", "ru", "Лёша"},
		{"Viktor Barinov", "ru", ""},
		{"waitress", "ru", ""},
		{"Viktor Barinov", "en", "Viktor Barinov"},
		{"Слава", "en", ""},
		{"Лёша / Alex", "en", ""},
		{"#2", "ru", "#2"},
		{"", "en", ""},
	} {
		if got := roleIn(c.role, c.lang); got != c.want {
			t.Errorf("roleIn(%q, %s) = %q, want %q", c.role, c.lang, got, c.want)
		}
	}
}
