package tmdb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/metadata"
)

// A path TMDB has no image for is asked once an hour, not on every view;
// concurrent views of one path download it once; a huge body is refused.
func TestPhotosFetchMissingOnceSingleflightLimited(t *testing.T) {
	var hits sync.Map // path → *atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := hits.LoadOrStore(r.URL.Path, new(atomic.Int32))
		n.(*atomic.Int32).Add(1)
		switch r.URL.Path {
		case "/t/p/w500/slow.png":
			<-release
			w.Write([]byte("PNG"))
		case "/t/p/w500/huge.png":
			w.Write([]byte(strings.Repeat("x", maxPhotoBytes+1)))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	count := func(p string) int32 {
		n, ok := hits.Load("/t/p/w500" + p)
		if !ok {
			return 0
		}
		return n.(*atomic.Int32).Load()
	}
	p := &Photos{Client: &Client{ImageBase: srv.URL + "/t/p"}, Images: &metadata.Images{Dir: t.TempDir()}, Size: ArtSize}
	ctx := context.Background()

	for range 3 {
		if _, err := p.Fetch(ctx, "/gone.png"); err == nil {
			t.Fatal("a 404 is an error")
		}
	}
	if count("/gone.png") != 1 {
		t.Fatalf("404 asked %d times", count("/gone.png"))
	}
	p.mu.Lock()
	p.missing["/gone.png"] = time.Now().Add(-2 * photoMissRetry) // an hour later
	p.mu.Unlock()
	p.Fetch(ctx, "/gone.png")
	if count("/gone.png") != 2 {
		t.Fatalf("not asked again after the hour: %d", count("/gone.png"))
	}

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Fetch(ctx, "/slow.png"); err != nil {
				t.Error(err)
			}
		}()
	}
	for count("/slow.png") == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // the others are waiting by now
	close(release)
	wg.Wait()
	if count("/slow.png") != 1 {
		t.Fatalf("downloaded %d times", count("/slow.png"))
	}

	if _, err := p.Fetch(ctx, "/huge.png"); err == nil {
		t.Fatal("over the limit")
	}
	if _, ok := p.Cached("/huge.png"); ok {
		t.Fatal("kept a refused body")
	}
}
