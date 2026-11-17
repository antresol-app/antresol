package wikidata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/metadata"
)

// Raw is metadata.Getter.RawWithin (tested there) with this client's base,
// User-Agent and an addonSem of size 2 that never holds back the core's
// own lookups.
func TestRawOwnBudget(t *testing.T) {
	var path, ua atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path.Store(r.URL.EscapedPath() + "?" + r.URL.RawQuery)
		ua.Store(r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/base", UserAgent: "Antresol/test", Limiter: &metadata.Limiter{}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, ct, _, err := c.Raw(ctx, "/a/b%20c", url.Values{"fmt": {"json"}})
	if err != nil || st != 200 || ct != "application/json" || path.Load() != "/base/a/b%20c?fmt=json" || ua.Load() != "Antresol/test" {
		t.Fatalf("%d %q %v path=%v ua=%v", st, ct, err, path.Load(), ua.Load())
	}
	if cap(c.addonSem) != 2 {
		t.Fatalf("addonSem size %d", cap(c.addonSem))
	}
	c.addonSem = make(chan struct{}, 1)
	c.addonSem <- struct{}{}
	if err := func() error { _, err := c.RottenTomatoes(ctx, "tt0816692"); return err }(); errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("core lookup blocked by addons: %v", err)
	}
	short, cancel2 := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel2()
	if _, _, _, err := c.Raw(short, "/x", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("addon call did not wait on addonSem: %v", err)
	}
}
