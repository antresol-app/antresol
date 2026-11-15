package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestVideosYouTubeTrailersCached(t *testing.T) {
	var hits atomic.Int32
	fail := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			w.WriteHeader(500)
			return
		}
		if r.URL.Path != "/3/movie/603/videos" || r.URL.Query().Get("include_video_language") == "" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"results":[
		  {"name":"Clip","site":"YouTube","key":"c1","type":"Clip","iso_639_1":"en","official":true},
		  {"name":"Vimeo","site":"Vimeo","key":"v1","type":"Trailer","iso_639_1":"en","official":true},
		  {"name":"Teaser","site":"YouTube","key":"t1","type":"Teaser","iso_639_1":"en","official":false},
		  {"name":"Трейлер","site":"YouTube","key":"r1","type":"Trailer","iso_639_1":"ru","official":true,"published_at":"2020-01-01T00:00:00.000Z"}]}`))
	}))
	defer srv.Close()
	rc := &Related{Client: &Client{BaseURL: srv.URL + "/3"}, DB: translationsDB(t)}
	t.Cleanup(rc.Close)
	ctx := context.Background()
	vs, ok := rc.Videos(ctx, "movie", 603)
	if !ok || len(vs) != 2 || vs[0].Key != "t1" || vs[1].Key != "r1" || vs[1].Lang != "ru" || !vs[1].Official {
		t.Fatalf("%v %+v", ok, vs)
	}
	// cached: TMDB is not asked again, even while failing
	fail.Store(true)
	if vs, ok := rc.Videos(ctx, "movie", 603); !ok || len(vs) != 2 || hits.Load() != 1 {
		t.Fatalf("%v %+v, %d hits", ok, vs, hits.Load())
	}
	// another kind with the same id is another entry; a failure is "none"
	if _, ok := rc.Videos(ctx, "tv", 603); ok {
		t.Fatal("tv 603 from the movie's entry")
	}
	if _, ok := rc.Videos(ctx, "person", 1); ok {
		t.Fatal("unknown kind")
	}
}

func TestVideosTTLIsADay(t *testing.T) {
	rc := &Related{}
	if rc.ttlOf("videos-movie") != 24*time.Hour || rc.ttlOf("movie") != relatedTTL {
		t.Fatalf("%v %v", rc.ttlOf("videos-movie"), rc.ttlOf("movie"))
	}
}
