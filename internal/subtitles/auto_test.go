package subtitles

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/media"
)

// fakeOS is an OpenSubtitles stand-in: one Russian and one English
// subtitle for every search, downloads return an srt (or quota).
type fakeOS struct {
	mu        sync.Mutex
	searches  []string // the languages of each search
	queries   []string // raw queries
	downloads []int
	quota     bool
	empty     bool
	srv       *httptest.Server
}

func newFakeOS(t *testing.T) *fakeOS {
	f := &fakeOS{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		switch r.URL.Path {
		case "/subtitles":
			f.searches = append(f.searches, r.URL.Query().Get("languages"))
			f.queries = append(f.queries, r.URL.RawQuery)
			if f.empty {
				io.WriteString(w, `{"data":[]}`)
				return
			}
			io.WriteString(w, `{"data":[
			 {"attributes":{"language":"ru","download_count":5,"release":"R","files":[{"file_id":101}]}},
			 {"attributes":{"language":"en","download_count":5,"release":"E","files":[{"file_id":201},{"file_id":202}]}}]}`)
		case "/download":
			var in struct {
				FileID int `json:"file_id"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			f.downloads = append(f.downloads, in.FileID)
			if f.quota {
				w.WriteHeader(406)
				io.WriteString(w, `{"remaining":-1}`)
				return
			}
			io.WriteString(w, `{"link":"`+f.srv.URL+`/f/`+strconv.Itoa(in.FileID)+`.srt","remaining":10}`)
		default:
			io.WriteString(w, "1\n00:00:01,000 --> 00:00:02,000\nfile "+strings.TrimSuffix(filepath.Base(r.URL.Path), ".srt")+"\n")
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeOS) counts() (searches []string, downloads []int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.searches...), append([]int(nil), f.downloads...)
}

type autoEnv struct {
	t     *testing.T
	db    *sql.DB
	repo  *library.Repo
	media string
	auto  *Auto
	fake  *fakeOS
	now   time.Time
	langs []string
}

func newAutoEnv(t *testing.T) *autoEnv {
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	e := &autoEnv{t: t, db: d, repo: library.NewRepo(d), media: t.TempDir(), fake: newFakeOS(t),
		now: time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC), langs: []string{"ru", "en"}}
	e.auto = &Auto{Client: &Client{BaseURL: e.fake.srv.URL, Key: "k", UserAgent: UserAgent}, Repo: e.repo, DB: d,
		Settings: func(context.Context) ([]string, bool) { return e.langs, true },
		DataDir:  t.TempDir(), MaxPerRun: 10, Now: func() time.Time { return e.now },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	return e
}

// movie adds a movie with a 200 KB file in its own folder and a probe.
func (e *autoEnv) movie(name string, tmdb int64, p media.ProbeResult) (dir string, fileID int64) {
	dir = filepath.Join(e.media, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.t.Fatal(err)
	}
	path := filepath.Join(dir, name+".mkv")
	if err := os.WriteFile(path, make([]byte, 200_000), 0o644); err != nil {
		e.t.Fatal(err)
	}
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: name, TmdbID: tmdb, File: library.FileInput{Path: path, Size: 200_000}})
	if err != nil {
		e.t.Fatal(err)
	}
	return dir, e.probe(id, p)
}

func (e *autoEnv) probe(itemID int64, p media.ProbeResult) int64 {
	fs, err := e.repo.FilesOf(context.Background(), itemID)
	if err != nil || len(fs) == 0 {
		e.t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	for _, f := range fs {
		if err := e.repo.SetProbe(context.Background(), f.ID, string(b), 1000); err != nil {
			e.t.Fatal(err)
		}
	}
	return fs[len(fs)-1].ID
}

func (e *autoEnv) run() {
	e.t.Helper()
	if err := e.auto.EnrichPending(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *autoEnv) attempt(fileID int64, lang string) string {
	var res string
	err := e.db.QueryRow(`SELECT result FROM subtitle_attempts WHERE file_id=? AND lang=?`, fileID, lang).Scan(&res)
	if err != nil && err != sql.ErrNoRows {
		e.t.Fatal(err)
	}
	return res
}

var (
	engAudio = media.ProbeResult{Container: "matroska", Audio: []media.AudioStream{{Index: 1, Codec: "aac", Lang: "eng", Default: true}}}
	undAudio = media.ProbeResult{Container: "matroska", Audio: []media.AudioStream{{Index: 1, Codec: "aac"}}}
	rusAudio = media.ProbeResult{Container: "matroska", Audio: []media.AudioStream{{Index: 1, Codec: "aac", Lang: "rus"}}}
)

func TestAutoSkipsWhenEmbeddedRussian(t *testing.T) {
	e := newAutoEnv(t)
	e.langs = []string{"ru"}
	p := engAudio
	p.Subs = []media.SubStream{{Index: 2, Codec: "subrip", Lang: "rus"}}
	e.movie("Дюна (2024)", 693134, p)
	e.run()
	if s, _ := e.fake.counts(); len(s) != 0 {
		t.Fatalf("searched %v", s)
	}
}

func TestAutoSkipsRussianOriginalAudio(t *testing.T) {
	e := newAutoEnv(t)
	e.movie("Брат (1997)", 20992, media.ProbeResult{Audio: []media.AudioStream{{Index: 1, Codec: "ac3", Lang: "rus"}, {Index: 2, Codec: "aac", Lang: "eng"}}})
	e.run()
	if s, _ := e.fake.counts(); len(s) != 1 || s[0] != "en" {
		t.Fatalf("searched %v", s)
	}
}

func TestAutoSkipsExternalAndForcedDoesNotCount(t *testing.T) {
	e := newAutoEnv(t)
	dir, _ := e.movie("Дюна (2024)", 693134, engAudio)
	os.WriteFile(filepath.Join(dir, "Дюна (2024).en.srt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "Дюна (2024).ru.forced.srt"), []byte("x"), 0o644)
	e.run()
	if s, _ := e.fake.counts(); len(s) != 1 || s[0] != "ru" {
		t.Fatalf("searched %v", s)
	}
}

func TestAutoWritesSidecar(t *testing.T) {
	e := newAutoEnv(t)
	e.langs = []string{"ru"}
	dir, f := e.movie("Дюна (2024)", 693134, engAudio)
	e.run()
	b, err := os.ReadFile(filepath.Join(dir, "Дюна (2024).ru.srt"))
	if err != nil || !strings.Contains(string(b), "file 101") {
		t.Fatalf("%q %v", b, err)
	}
	if r := e.attempt(f, "ru"); r != "found" {
		t.Fatalf("attempt %q", r)
	}
	s, _ := e.fake.counts()
	if len(s) != 1 || !strings.Contains(e.fake.queries[0], "moviehash=") || !strings.Contains(e.fake.queries[0], "tmdb_id=693134") {
		t.Fatalf("%v %v", s, e.fake.queries)
	}
	e.run() // the sidecar is there now: nothing to do
	if s, _ := e.fake.counts(); len(s) != 1 {
		t.Fatalf("searched again %v", s)
	}
}

func TestAutoReadOnlyFallsBackToDataDir(t *testing.T) {
	e := newAutoEnv(t)
	e.langs = []string{"ru"}
	dir, f := e.movie("Дюна (2024)", 693134, engAudio)
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	e.run()
	p := filepath.Join(e.auto.DataDir, "subs", "ext", strconv.FormatInt(f, 10)+".ru.srt")
	if b, err := os.ReadFile(p); err != nil || !strings.Contains(string(b), "file 101") {
		t.Fatalf("%q %v", b, err)
	}
	ext := FindExternal(filepath.Join(dir, "Дюна (2024).mkv"), e.auto.DataDir, f)
	if len(ext) != 1 || ext[0].Path != p || ext[0].Lang != "ru" {
		t.Fatalf("%+v", ext)
	}
}

func TestAutoQuotaStopsRun(t *testing.T) {
	e := newAutoEnv(t)
	e.langs = []string{"ru"}
	e.fake.quota = true
	_, f1 := e.movie("Первый (2001)", 1, engAudio)
	_, f2 := e.movie("Второй (2002)", 2, engAudio)
	e.run()
	s, d := e.fake.counts()
	if len(s) != 1 || len(d) != 1 {
		t.Fatalf("after quota: %v %v", s, d)
	}
	if e.attempt(f2, "ru") != "quota" || e.attempt(f1, "ru") != "" {
		t.Fatalf("attempts %q %q (newest first)", e.attempt(f2, "ru"), e.attempt(f1, "ru"))
	}
	e.now = time.Date(2026, 10, 25, 23, 59, 0, 0, time.UTC)
	e.run()
	if s, _ := e.fake.counts(); len(s) != 1 {
		t.Fatalf("before midnight UTC: %v", s)
	}
	e.fake.mu.Lock()
	e.fake.quota = false
	e.fake.mu.Unlock()
	e.now = time.Date(2026, 10, 26, 0, 1, 0, 0, time.UTC)
	e.run()
	s, _ = e.fake.counts()
	if len(s) != 3 || e.attempt(f1, "ru") != "found" || e.attempt(f2, "ru") != "found" {
		t.Fatalf("after midnight: %v %q %q", s, e.attempt(f1, "ru"), e.attempt(f2, "ru"))
	}
}

func TestAutoOffline(t *testing.T) {
	e := newAutoEnv(t)
	e.fake.srv.Close()
	_, f := e.movie("Дюна (2024)", 693134, engAudio)
	if err := e.auto.EnrichPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.attempt(f, "ru") != "" || e.attempt(f, "en") != "" {
		t.Fatal("attempt written while offline")
	}
}

func TestAutoNoRetryWithin7Days(t *testing.T) {
	e := newAutoEnv(t)
	e.fake.empty = true
	_, f := e.movie("Дюна (2024)", 693134, undAudio)
	e.run()
	if e.attempt(f, "ru") != "none" || e.attempt(f, "en") != "none" {
		t.Fatalf("%q %q", e.attempt(f, "ru"), e.attempt(f, "en"))
	}
	e.now = e.now.Add(6 * 24 * time.Hour)
	e.run()
	if s, _ := e.fake.counts(); len(s) != 1 {
		t.Fatalf("retried within 7 days: %v", s)
	}
	e.now = e.now.Add(2 * 24 * time.Hour)
	e.run()
	if s, _ := e.fake.counts(); len(s) != 2 {
		t.Fatalf("not retried after 7 days: %v", s)
	}
}

func TestAutoMaxPerRunCountsSearches(t *testing.T) {
	e := newAutoEnv(t)
	e.fake.empty = true
	e.auto.MaxPerRun = 2
	for i := 0; i < 3; i++ {
		e.movie("Фильм "+strconv.Itoa(i), int64(i+1), engAudio)
	}
	e.run()
	if s, _ := e.fake.counts(); len(s) != 2 {
		t.Fatalf("%v", s)
	}
	e.run()
	if s, _ := e.fake.counts(); len(s) != 3 {
		t.Fatalf("second run: %v", s)
	}
}

func TestAutoEpisodeQuery(t *testing.T) {
	e := newAutoEnv(t)
	e.fake.empty = true
	e.langs = []string{"en"}
	path := filepath.Join(e.media, "Сёгун", "Season 01", "Сёгун - S01E03.mkv")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, make([]byte, 1000), 0o644) // too small to hash
	id, err := e.repo.UpsertEpisode(context.Background(), library.EpisodeInput{SeriesTitle: "Сёгун", SeriesTmdbID: 126308, Season: 1, Episode: 3,
		File: library.FileInput{Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	e.probe(id, rusAudio)
	e.run()
	if len(e.fake.queries) != 1 || e.fake.queries[0] != "episode_number=3&languages=en&parent_tmdb_id=126308&season_number=1" {
		t.Fatalf("%v", e.fake.queries)
	}
}

func TestAutoTwoPartMovie(t *testing.T) {
	e := newAutoEnv(t)
	e.langs = []string{"en"}
	dir := filepath.Join(e.media, "Москва слезам не верит (1979)")
	os.MkdirAll(dir, 0o755)
	var paths []string
	for _, n := range []string{"cd1", "cd2"} {
		p := filepath.Join(dir, "Москва слезам не верит (1979) - "+n+".mkv")
		os.WriteFile(p, make([]byte, 200_000), 0o644)
		paths = append(paths, p)
	}
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "Москва слезам не верит", TmdbID: 21028,
		Files: []library.FileInput{{Path: paths[0]}, {Path: paths[1]}}})
	if err != nil {
		t.Fatal(err)
	}
	e.probe(id, rusAudio)
	e.run()
	for i, p := range paths {
		b, err := os.ReadFile(strings.TrimSuffix(p, ".mkv") + ".en.srt")
		if want := "file 20" + strconv.Itoa(i+1); err != nil || !strings.Contains(string(b), want) {
			t.Fatalf("part %d: %q %v (want %s)", i+1, b, err, want)
		}
	}
}

func TestAutoUnidentifiedFileIsNotSearched(t *testing.T) {
	e := newAutoEnv(t)
	path := filepath.Join(e.media, "x.mkv")
	os.WriteFile(path, make([]byte, 10), 0o644) // no hash, no TMDB id
	id, err := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "x", File: library.FileInput{Path: path}})
	if err != nil {
		t.Fatal(err)
	}
	f := e.probe(id, undAudio)
	e.run()
	if s, _ := e.fake.counts(); len(s) != 0 || e.attempt(f, "ru") != "none" {
		t.Fatalf("%v %q", s, e.attempt(f, "ru"))
	}
}

func TestAutoAuthRefusedStopsWithoutAttempts(t *testing.T) {
	e := newAutoEnv(t)
	e.langs = []string{"ru"}
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			w.WriteHeader(401)
			return
		}
		e.fake.srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer login.Close()
	e.auto.Client = &Client{BaseURL: login.URL, Key: "k", Username: "u", Password: "wrong"}
	_, f1 := e.movie("Первый (2001)", 1, engAudio)
	_, f2 := e.movie("Второй (2002)", 2, engAudio)
	e.run()
	if s, d := e.fake.counts(); len(s) != 1 || len(d) != 0 {
		t.Fatalf("searches %v downloads %v", s, d)
	}
	var n int
	e.db.QueryRow(`SELECT COUNT(*) FROM subtitle_attempts`).Scan(&n)
	if n != 0 || e.attempt(f1, "ru") != "" || e.attempt(f2, "ru") != "" {
		t.Fatalf("%d attempts", n)
	}
}

func TestAutoBudgetCountsOnlySearches(t *testing.T) {
	e := newAutoEnv(t)
	e.fake.empty = true
	e.langs = []string{"ru"}
	e.auto.MaxPerRun = 1
	e.movie("Найден (2001)", 1, engAudio) // older, has a TMDB id
	path := filepath.Join(e.media, "x.mkv")
	os.WriteFile(path, make([]byte, 10), 0o644)
	id, _ := e.repo.UpsertMovie(context.Background(), library.MovieInput{Title: "x", File: library.FileInput{Path: path}})
	e.probe(id, engAudio) // newest, cannot be searched
	e.run()
	if s, _ := e.fake.counts(); len(s) != 1 {
		t.Fatalf("%v", s)
	}
}

func TestAutoExternalUkrainianDoesNotCoverRussian(t *testing.T) {
	e := newAutoEnv(t)
	e.langs = []string{"ru"}
	dir, _ := e.movie("Дюна (2024)", 693134, engAudio)
	os.WriteFile(filepath.Join(dir, "Дюна (2024).uk.srt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "Дюна (2024).rus.srt"), []byte("x"), 0o644)
	e.run()
	if s, _ := e.fake.counts(); len(s) != 0 {
		t.Fatalf("rus external must cover ru: %v", s)
	}
}

func TestAutoQuotaSharedWithClient(t *testing.T) {
	e := newAutoEnv(t)
	e.auto.Client.PauseUntil(e.now.Add(time.Hour)) // a manual download hit the quota
	e.movie("Дюна (2024)", 693134, engAudio)
	e.run()
	if s, _ := e.fake.counts(); len(s) != 0 {
		t.Fatalf("%v", s)
	}
}
