package segments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestLookupKey(t *testing.T) {
	l := Lookup{TmdbID: 1418, IMDb: "tt0898266", Season: 1, Episode: 2, DurationMs: 1_299_000}
	// TheIntroDB answers for the release closest to our duration: one key per minute of it
	if k := l.key(SourceTheIntroDB); k != "tmdb:1418/1/2@21m" {
		t.Fatal(k)
	}
	l2 := l
	l2.DurationMs = 1_420_000
	if l2.key(SourceTheIntroDB) == l.key(SourceTheIntroDB) {
		t.Fatal("another release, the same key")
	}
	// an episode without a number is not looked up (it would be episode 0)
	if k := (Lookup{TmdbID: 1418, IMDb: "tt0898266", Season: 1}).key(SourceTheIntroDB); k != "" {
		t.Fatal(k)
	}
	if k := (Lookup{TmdbID: 1418, IMDb: "tt0898266", Season: 1}).key(SourceIntroDB); k != "" {
		t.Fatal(k)
	}
	if k := l.key(SourceIntroDB); k != "imdb:tt0898266/1/2" {
		t.Fatal(k)
	}
	if k := (Lookup{TmdbID: 603, Movie: true}).key(SourceTheIntroDB); k != "tmdb:603@0m" {
		t.Fatal(k)
	}
	if k := (Lookup{TmdbID: 603, Movie: true}).key(SourceIntroDB); k != "" {
		t.Fatal(k)
	}
}

func TestAskTheIntroDB(t *testing.T) {
	var ua, query atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua.Store(r.UserAgent())
		query.Store(r.URL.RawQuery)
		switch r.URL.Query().Get("episode") {
		case "2":
			w.Write([]byte(`{"tvdb_id":80379,"type":"tv","season":1,"episode":2,"group_duration_ms":1300000,
				"intro":[{"start_ms":223600,"end_ms":243600}],"credits":[{"start_ms":1238529,"end_ms":null}],
				"recap":[{"start_ms":null,"end_ms":40000}],"preview":[{"start_ms":1,"end_ms":2}]}`))
		case "3":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"no submissions for this media","code":"no_submissions"}`))
		case "4":
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	defer srv.Close()
	o := &Online{TheIntroDB: srv.URL, UA: "Antresol/test"}
	ctx := context.Background()
	a, err := o.AskTheIntroDB(ctx, Lookup{TmdbID: 1418, Season: 1, Episode: 2}, 1_299_000)
	if err != nil {
		t.Fatal(err)
	}
	if ua.Load() != "Antresol/test" || query.Load() != "details=true&duration_ms=1299000&episode=2&season=1&tmdb_id=1418" {
		t.Fatalf("ua %v query %v", ua.Load(), query.Load())
	}
	want := Answer{GroupMs: 1_300_000, Segments: []Segment{
		{Recap, SourceTheIntroDB, 0, 40000}, {Intro, SourceTheIntroDB, 223600, 243600}, {Credits, SourceTheIntroDB, 1238529, -1}}}
	if !reflect.DeepEqual(a, want) {
		t.Fatalf("%+v", a)
	}
	if a, err := o.AskTheIntroDB(ctx, Lookup{TmdbID: 1418, Season: 1, Episode: 3}, 0); err != nil || len(a.Segments) != 0 {
		t.Fatalf("404: %+v %v", a, err)
	}
	if _, err := o.AskTheIntroDB(ctx, Lookup{TmdbID: 1418, Season: 1, Episode: 4}, 0); !errors.Is(err, ErrLimited) {
		t.Fatalf("429: %v", err)
	}
	if _, err := o.AskTheIntroDB(ctx, Lookup{TmdbID: 1418, Season: 1, Episode: 5}, 0); err == nil || errors.Is(err, ErrLimited) || errors.Is(err, ErrNetwork) {
		t.Fatalf("502: %v", err)
	}
	// no answer at all: a network failure
	dead := &Online{TheIntroDB: "http://127.0.0.1:1"}
	if _, err := dead.AskTheIntroDB(ctx, Lookup{TmdbID: 1418, Season: 1, Episode: 2}, 0); !errors.Is(err, ErrNetwork) {
		t.Fatalf("refused: %v", err)
	}
}

func TestAskIntroDB(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/segments" || r.URL.Query().Get("imdb_id") != "tt0898266" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"imdb_id":"tt0898266","media_type":"tv","season":1,"episode":1,"intro":null,"recap":null,
			"outro":{"start_sec":1344,"end_sec":1369,"start_ms":1344000,"end_ms":1369000,"confidence":1},"post_credits":null}`))
	}))
	defer srv.Close()
	o := &Online{IntroDB: srv.URL}
	a, err := o.AskIntroDB(context.Background(), Lookup{IMDb: "tt0898266", Season: 1, Episode: 1})
	if err != nil || !reflect.DeepEqual(a.Segments, []Segment{{Credits, SourceIntroDB, 1344000, 1369000}}) {
		t.Fatalf("%+v %v", a, err)
	}
	if a, err := o.AskIntroDB(context.Background(), Lookup{TmdbID: 1}); err != nil || len(a.Segments) != 0 {
		t.Fatalf("no imdb: %+v %v", a, err)
	}
}

func TestDailyCap(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	o := &Online{TheIntroDB: srv.URL}
	o.spend(SourceTheIntroDB)
	o.day[SourceTheIntroDB] = dayCap
	if _, err := o.AskTheIntroDB(context.Background(), Lookup{TmdbID: 1, Movie: true}, 0); !errors.Is(err, ErrLimited) || n.Load() != 0 {
		t.Fatalf("over the cap: %v, %d requests", err, n.Load())
	}
}

func TestAnswerForFile(t *testing.T) {
	a := Answer{GroupMs: 1_300_000, Segments: []Segment{
		{Credits, SourceTheIntroDB, 300_000, 400_000}, // a credits mark in the first half: not the end credits
		{Intro, SourceTheIntroDB, 60_000, 90_000},
		{Intro, SourceTheIntroDB, 500_000, 530_000},
		{Credits, SourceTheIntroDB, 1_250_000, -1},
	}}
	got := a.ForFile(1_290_000)
	want := []Segment{{Intro, SourceTheIntroDB, 60_000, 90_000}, {Credits, SourceTheIntroDB, 1_250_000, 1_290_000}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%+v", got)
	}
	// another release (4 % longer): not used
	if got := a.ForFile(1_250_000); got != nil {
		t.Fatalf("drift: %+v", got)
	}
	// a segment past the end of the file: not used
	b := Answer{Segments: []Segment{{Intro, SourceIntroDB, 60_000, 90_000}, {Credits, SourceIntroDB, 1_250_000, 1_400_000}}}
	if got := b.ForFile(1_300_000); got != nil {
		t.Fatalf("past the end: %+v", got)
	}
}
