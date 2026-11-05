package metadata

import (
	"context"
	"github.com/antresol-app/antresol/internal/version"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestImagesCached(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("IMG"))
	}))
	defer srv.Close()
	im := &Images{Dir: filepath.Join(t.TempDir(), "images")}
	n1, err := im.Fetch(context.Background(), srv.URL+"/t/p/w500/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	n2, err := im.Fetch(context.Background(), srv.URL+"/t/p/w500/a.jpg")
	if err != nil || n1 != n2 || hits.Load() != 1 {
		t.Fatalf("%q %q %v hits=%d", n1, n2, err, hits.Load())
	}
	if b, _ := os.ReadFile(filepath.Join(im.Dir, n1)); string(b) != "IMG" || !strings.HasSuffix(n1, ".jpg") {
		t.Fatalf("%q %q", n1, b)
	}
	if n, _ := im.Fetch(context.Background(), srv.URL+"/l.png"); !strings.HasSuffix(n, ".png") {
		t.Fatalf("png ext: %q", n)
	}
}

func TestImagesHTTPErrorNotCached(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	im := &Images{Dir: t.TempDir()}
	if _, err := im.Fetch(context.Background(), srv.URL+"/x.jpg"); err == nil {
		t.Fatal("want error")
	}
	if es, _ := os.ReadDir(im.Dir); len(es) != 0 {
		t.Fatalf("leftovers: %v", es)
	}
}

func TestUserAgentSent(t *testing.T) {
	ua := version.UserAgent
	var got atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.Store(r.Header.Get("User-Agent"))
		w.Write([]byte("IMG"))
	}))
	defer srv.Close()
	im := &Images{Dir: t.TempDir(), UserAgent: ua}
	if _, err := im.Fetch(context.Background(), srv.URL+"/a.jpg"); err != nil {
		t.Fatal(err)
	}
	if got.Load() != ua {
		t.Fatalf("User-Agent %q", got.Load())
	}
}
