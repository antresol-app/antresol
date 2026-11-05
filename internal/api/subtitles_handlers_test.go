package api

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/antresol-app/antresol/internal/subtitles"
)

func fakeOpenSubtitles(t *testing.T, quota bool, downloadCalls *atomic.Int32) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/subtitles":
			io.WriteString(w, `{"data":[
			 {"attributes":{"language":"ru","download_count":7,"moviehash_match":true,"release":"Movie.2020.WEB","files":[{"file_id":42}]}},
			 {"attributes":{"language":"en","download_count":9,"release":"Movie.EN","files":[{"file_id":43}]}}]}`)
		case "/download":
			downloadCalls.Add(1)
			if quota {
				w.WriteHeader(406)
				return
			}
			io.WriteString(w, `{"link":"`+srv.URL+`/f.srt","remaining":5}`)
		case "/f.srt":
			io.WriteString(w, "1\n00:00:01,000 --> 00:00:02,000\nПривет\n")
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func subsEnv(t *testing.T, quota bool) *libEnv {
	return subsEnvCount(t, quota, new(atomic.Int32))
}

func subsEnvCount(t *testing.T, quota bool, downloadCalls *atomic.Int32) *libEnv {
	srv := fakeOpenSubtitles(t, quota, downloadCalls)
	return newLibEnvWith(t, func(d *Deps, _ *sql.DB) {
		d.Subtitles = &subtitles.Client{BaseURL: srv.URL, Key: "k"}
	})
}

func TestSubtitlesAPIAdminOnly(t *testing.T) {
	e := subsEnv(t, false)
	_, f := e.movie("Movie", "0+", strings.Repeat("x", 10))
	user := e.login(`{"name":"Мама"}`)
	if w := e.do("GET", "/api/subtitles/"+itoa(f)+"/search?lang=ru", "", user); w.Code != http.StatusForbidden {
		t.Fatalf("search %d", w.Code)
	}
	if w := e.do("POST", "/api/subtitles/"+itoa(f), `{"subtitleId":42,"lang":"ru"}`, user); w.Code != http.StatusForbidden {
		t.Fatalf("download %d", w.Code)
	}
	if w := e.do("GET", "/api/subtitles/"+itoa(f)+"/search?lang=ru", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anon %d", w.Code)
	}
}

func TestSubtitlesAPISearchAndDownload(t *testing.T) {
	e := subsEnv(t, false)
	id, f := e.movie("Movie", "0+", "x")
	if _, err := e.db.Exec(`UPDATE items SET tmdb_id=438631 WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	e.probeJSON(f, `{"Container":"mp4","Video":{"Codec":"h264","Height":720,"BitDepth":8},"Audio":[{"Index":1,"Codec":"aac","Default":true}]}`)
	w := e.do("GET", "/api/subtitles/"+itoa(f)+"/search?lang=ru", "", e.admin)
	var out struct {
		Results []struct {
			SubtitleID int    `json:"subtitleId"`
			Lang       string `json:"lang"`
			Release    string `json:"release"`
			Downloads  int    `json:"downloads"`
			HashMatch  bool   `json:"hashMatch"`
		} `json:"results"`
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || len(out.Results) != 1 || out.Results[0].SubtitleID != 42 || out.Results[0].Release != "Movie.2020.WEB" ||
		!out.Results[0].HashMatch || out.Results[0].Downloads != 7 || out.Results[0].Lang != "ru" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if w := e.do("GET", "/api/subtitles/"+itoa(f)+"/search?lang=xx", "", e.admin); w.Code != 400 || !strings.Contains(w.Body.String(), "Неизвестный язык субтитров") {
		t.Fatalf("xx: %d %s", w.Code, w.Body)
	}
	if w := e.do("POST", "/api/subtitles/"+itoa(f), `{"subtitleId":42,"lang":"ru"}`, e.admin); w.Code != http.StatusNoContent {
		t.Fatalf("download %d %s", w.Code, w.Body)
	}
	b, err := os.ReadFile(filepath.Join(e.media, "Movie.ru.srt"))
	if err != nil || !strings.Contains(string(b), "Привет") {
		t.Fatalf("%q %v", b, err)
	}
	if subs := e.playSubs(f); len(subs) != 1 || subs[0].Lang != "ru" {
		t.Fatalf("%+v", subs)
	}
}

func TestSubtitlesAPINotConfiguredAndQuota(t *testing.T) {
	e := newLibEnv(t)
	_, f := e.movie("Movie", "0+", "x")
	for _, c := range []struct{ m, p, b string }{{"GET", "/api/subtitles/" + itoa(f) + "/search?lang=ru", ""}, {"POST", "/api/subtitles/" + itoa(f), `{"subtitleId":42,"lang":"ru"}`}} {
		w := e.do(c.m, c.p, c.b, e.admin)
		if w.Code != 503 || !strings.Contains(w.Body.String(), "Поиск субтитров не настроен: нужен ключ OpenSubtitles") {
			t.Fatalf("%s: %d %s", c.m, w.Code, w.Body)
		}
	}
	var downloadCalls atomic.Int32
	q := subsEnvCount(t, true, &downloadCalls)
	_, f = q.movie("Movie", "0+", "x")
	for i := 0; i < 2; i++ { // the second is answered from the shared pause
		w := q.do("POST", "/api/subtitles/"+itoa(f), `{"subtitleId":42,"lang":"ru"}`, q.admin)
		if w.Code != 429 || !strings.Contains(w.Body.String(), "Лимит загрузок OpenSubtitles на сегодня исчерпан") {
			t.Fatalf("quota %d: %d %s", i, w.Code, w.Body)
		}
	}
	if n := downloadCalls.Load(); n != 1 {
		t.Fatalf("%d download calls", n)
	}
}
