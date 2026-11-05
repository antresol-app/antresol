package subtitles

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const searchJSON = `{"total_count":3,"data":[
 {"id":"1","type":"subtitle","attributes":{"language":"ru","download_count":900,"hearing_impaired":false,"moviehash_match":false,"release":"Dune.2024.WEB","files":[{"file_id":11,"file_name":"a.srt"}]}},
 {"id":"2","type":"subtitle","attributes":{"language":"ru","download_count":10,"hearing_impaired":false,"moviehash_match":true,"release":"Dune.2024.1080p","files":[{"file_id":22,"file_name":"b.srt"}]}},
 {"id":"3","type":"subtitle","attributes":{"language":"en","download_count":5000,"hearing_impaired":true,"release":"Dune.SDH","files":[{"file_id":33},{"file_id":34}]}}
]}`

func TestSearchSendsHeaders(t *testing.T) {
	var seen *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(context.Background())
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, searchJSON)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k123", UserAgent: UserAgent}
	subs, err := c.Search(context.Background(), Query{MovieHash: "8e245d9679d31e12", TMDBID: 693134, Languages: []string{"ru", "en", "pt"}})
	if err != nil {
		t.Fatal(err)
	}
	if seen.URL.Path != "/subtitles" || seen.Method != "GET" {
		t.Fatalf("%s %s", seen.Method, seen.URL.Path)
	}
	if seen.Header.Get("Api-Key") != "k123" || seen.Header.Get("User-Agent") != "Vault v0.1" {
		t.Fatalf("headers %v", seen.Header)
	}
	q := seen.URL.Query()
	if q.Get("moviehash") != "8e245d9679d31e12" || q.Get("languages") != "en,pt-br,pt-pt,ru" || q.Get("tmdb_id") != "693134" {
		t.Fatalf("query %v", q)
	}
	if !strings.Contains(seen.URL.RawQuery, "languages=en,pt-br,pt-pt,ru") {
		t.Fatalf("raw query %s", seen.URL.RawQuery) // commas unescaped: no redirect at OpenSubtitles
	}
	if s := subs[1]; len(subs) != 3 || s.FileID != 22 || s.Lang != "ru" || s.Release != "Dune.2024.1080p" || s.Downloads != 10 || !s.HashMatch || s.HearingImpaired {
		t.Fatalf("%+v", subs)
	}
	if s := subs[2]; s.FileID != 33 || !s.HearingImpaired || len(s.Files) != 2 || s.Downloads != 5000 {
		t.Fatalf("%+v", s)
	}
}

func TestSearchEpisodeQuery(t *testing.T) {
	var raw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.URL.RawQuery
		io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k"}
	if _, err := c.Search(context.Background(), Query{ParentTMDBID: 1399, Season: 2, Episode: 3, Languages: []string{"en"}}); err != nil {
		t.Fatal(err)
	}
	if raw != "episode_number=3&languages=en&parent_tmdb_id=1399&season_number=2" {
		t.Fatalf("%s", raw)
	}
}

func TestBestPrefersHashMatch(t *testing.T) {
	subs := []Subtitle{
		{FileID: 1, Lang: "ru", Downloads: 900},
		{FileID: 2, Lang: "ru", Downloads: 10, HashMatch: true, HearingImpaired: true},
		{FileID: 3, Lang: "en", Downloads: 5000, HashMatch: true},
	}
	if b, ok := Best(subs, "ru"); !ok || b.FileID != 2 {
		t.Fatalf("%+v %v", b, ok)
	}
	subs[1].HashMatch = false
	subs = append(subs, Subtitle{FileID: 4, Lang: "ru", Downloads: 50})
	if b, _ := Best(subs, "ru"); b.FileID != 1 { // hearing impaired loses; then downloads
		t.Fatalf("%+v", b)
	}
	if _, ok := Best(subs, "de"); ok {
		t.Fatal("no german")
	}
}

func TestDownloadQuota(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(406)
		io.WriteString(w, `{"requests":6,"remaining":-1,"message":"You have downloaded your allowed 5 subtitles for 24h"}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k"}
	if _, err := c.Download(context.Background(), 11); !errors.Is(err, ErrQuota) {
		t.Fatalf("%v", err)
	}
}

func TestDownloadLoginAndLink(t *testing.T) {
	var logins atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			logins.Add(1)
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"username":"u"`) || r.Header.Get("Api-Key") != "k" {
				w.WriteHeader(401)
				return
			}
			io.WriteString(w, `{"token":"tok","status":200}`)
		case "/download":
			b, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer tok" || !strings.Contains(string(b), `"file_id":11`) ||
				!strings.Contains(string(b), `"sub_format":"srt"`) {
				w.WriteHeader(400)
				return
			}
			rem := "4"
			if strings.Contains(string(b), `"file_id":12`) {
				rem = "0"
			}
			io.WriteString(w, `{"link":"`+srv.URL+`/file/x.srt","remaining":`+rem+`}`)
		case "/file/x.srt":
			io.WriteString(w, "1\n00:00:01,000 --> 00:00:02,000\nПривет\n")
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k", Username: "u", Password: "p"}
	for i := 0; i < 2; i++ {
		b, err := c.Download(context.Background(), 11)
		if err != nil || !strings.Contains(string(b), "Привет") {
			t.Fatalf("%q %v", b, err)
		}
	}
	if logins.Load() != 1 {
		t.Fatalf("token not cached: %d logins", logins.Load())
	}
}

func TestDownloadLastAllowedIsQuotaWithData(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download":
			io.WriteString(w, `{"link":"`+srv.URL+`/f.srt","remaining":0}`)
		case "/f.srt":
			io.WriteString(w, "subs")
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k"}
	b, err := c.Download(context.Background(), 1)
	if !errors.Is(err, ErrQuota) || string(b) != "subs" {
		t.Fatalf("%q %v", b, err)
	}
}

func TestSearchOffline(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", Key: "k"}
	if _, err := c.Search(context.Background(), Query{TMDBID: 1, Languages: []string{"ru"}}); !errors.Is(err, ErrOffline) {
		t.Fatalf("%v", err)
	}
}

func TestSearchFilesInDiscOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"data":[{"attributes":{"language":"en","files":[{"file_id":8,"cd_number":2},{"file_id":7,"cd_number":1}]}}]}`)
	}))
	defer srv.Close()
	subs, err := (&Client{BaseURL: srv.URL, Key: "k"}).Search(context.Background(), Query{TMDBID: 1, Languages: []string{"en"}})
	if err != nil || len(subs) != 1 || subs[0].FileID != 7 || subs[0].Files[1] != 8 {
		t.Fatalf("%+v %v", subs, err)
	}
}

func TestDownloadReloginOn401(t *testing.T) {
	var logins, downloads atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			n := logins.Add(1)
			io.WriteString(w, `{"token":"tok`+strconv.Itoa(int(n))+`"}`)
		case "/download":
			downloads.Add(1)
			if r.Header.Get("Authorization") != "Bearer tok2" {
				w.WriteHeader(401) // the first token expired early
				return
			}
			io.WriteString(w, `{"link":"`+srv.URL+`/f.srt","remaining":3}`)
		case "/f.srt":
			io.WriteString(w, "x")
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k", Username: "u", Password: "p"}
	if b, err := c.Download(context.Background(), 1); err != nil || string(b) != "x" || logins.Load() != 2 || downloads.Load() != 2 {
		t.Fatalf("%q %v logins %d downloads %d", b, err, logins.Load(), downloads.Load())
	}
}

func TestDownloadAuthRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer srv.Close()
	for _, c := range []*Client{{BaseURL: srv.URL, Key: "k", Username: "u", Password: "bad"}, {BaseURL: srv.URL, Key: "bad"}} {
		if _, err := c.Download(context.Background(), 1); !errors.Is(err, ErrAuth) {
			t.Fatalf("%+v: %v", c.Username, err)
		}
	}
	if _, err := (&Client{BaseURL: srv.URL, Key: "bad"}).Search(context.Background(), Query{TMDBID: 1}); !errors.Is(err, ErrAuth) {
		t.Fatalf("search: %v", err)
	}
}

func TestQuotaResetTimeFromServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(406)
		io.WriteString(w, `{"remaining":-1,"reset_time_utc":"2026-10-25T21:10:00.000Z"}`)
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Key: "k"}
	if _, err := c.Download(context.Background(), 1); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 15, 0, 0, 0, time.UTC)
	if got := c.QuotaReset(now); !got.Equal(time.Date(2026, 10, 25, 21, 10, 0, 0, time.UTC)) {
		t.Fatalf("%v", got)
	}
	if got := (&Client{}).QuotaReset(now); !got.Equal(time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("fallback %v", got)
	}
}

func TestDownloadRejectsOddLinks(t *testing.T) {
	for _, link := range []string{"file:///etc/passwd", "ftp://x/y.srt", "/relative.srt"} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"link":"`+link+`","remaining":3}`)
		}))
		_, err := (&Client{BaseURL: srv.URL, Key: "k"}).Download(context.Background(), 1)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "unexpected link") {
			t.Fatalf("%s: %v", link, err)
		}
	}
	if _, err := (&Client{BaseURL: "https://api.example"}).fetch(context.Background(), "http://dl.example/a.srt"); err == nil || !strings.Contains(err.Error(), "unexpected link") {
		t.Fatalf("http link from an https API: %v", err)
	}
}
