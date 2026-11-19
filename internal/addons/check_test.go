package addons

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// realGH answers like GitHub does today: the API's release JSON (with the
// fields GitHub sends), browser_download_url on the release page path,
// which redirects (302) to a signed URL of the asset CDN
// (release-assets.githubusercontent.com). The test server stands in for
// every host, so it is the resolver's API host.
type realGH struct {
	srv       *httptest.Server
	apiHits   atomic.Int32
	maniHits  atomic.Int32
	manifest  []byte
	maniDelay time.Duration // before the CDN answers the manifest
	maniFail  atomic.Int32  // so many manifest answers are 503 first
	apiStatus int           // set: the API answers this, with apiHeader
	apiHeader http.Header
	blobs     map[string][]byte // asset name → bytes (served with ranges)
	cut       atomic.Int32      // so many asset answers break off halfway
}

func newRealGH(t *testing.T, m Manifest) *realGH {
	f := &realGH{blobs: map[string][]byte{}}
	f.manifest, _ = json.Marshal(m)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.apiHits.Add(1)
		if f.apiStatus != 0 {
			for k, v := range f.apiHeader {
				w.Header()[k] = v
			}
			w.WriteHeader(f.apiStatus)
			fmt.Fprint(w, `{"message":"API rate limit exceeded","documentation_url":"https://docs.github.com/rest"}`)
			return
		}
		base := f.srv.URL
		type asset struct {
			URL         string `json:"url"`
			ID          int    `json:"id"`
			Name        string `json:"name"`
			ContentType string `json:"content_type"`
			State       string `json:"state"`
			Size        int    `json:"size"`
			Download    string `json:"browser_download_url"`
		}
		as := []asset{{URL: base + "/repos/o/r/releases/assets/1", ID: 1, Name: ManifestName, ContentType: "application/json",
			State: "uploaded", Size: len(f.manifest), Download: base + "/o/r/releases/download/v1.1.0/" + ManifestName}}
		for n, b := range f.blobs {
			as = append(as, asset{URL: base + "/repos/o/r/releases/assets/2", ID: 2, Name: n, ContentType: "application/gzip",
				State: "uploaded", Size: len(b), Download: base + "/o/r/releases/download/v1.1.0/" + n})
		}
		json.NewEncoder(w).Encode(map[string]any{
			"url": base + "/repos/o/r/releases/1", "html_url": "https://github.com/o/r/releases/tag/v1.1.0", "id": 1,
			"tag_name": "v1.1.0", "target_commitish": "main", "name": "v1.1.0", "draft": false, "prerelease": false,
			"created_at": "2026-11-18T10:00:00Z", "published_at": "2026-11-18T10:05:00Z", "assets": as,
			"tarball_url": base + "/repos/o/r/tarball/v1.1.0", "body": "notes",
			"author": map[string]any{"login": "bot", "id": 1},
		})
	})
	// github.com/<o>/<r>/releases/download/<tag>/<name> → the CDN
	mux.HandleFunc("/o/r/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		http.Redirect(w, r, "/github-production-release-asset/1462496856/x-"+name+
			"?sp=r&sv=2018-11-09&sr=b&spr=https&se=2026-11-19T13%3A34%3A24Z&rscd=attachment%3B+filename%3D"+name+
			"&rsct=application%2Foctet-stream&sig=abc%3D", http.StatusFound)
	})
	mux.HandleFunc("/github-production-release-asset/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], "x-")
		if name == ManifestName {
			f.maniHits.Add(1)
			if f.maniFail.Load() > 0 {
				f.maniFail.Add(-1)
				http.Error(w, "busy", http.StatusServiceUnavailable)
				return
			}
			time.Sleep(f.maniDelay)
			w.Write(f.manifest)
			return
		}
		b, ok := f.blobs[name]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Range") == "" && f.cut.Load() > 0 {
			f.cut.Add(-1)
			w.Header().Set("Content-Length", fmt.Sprint(len(b)))
			w.Write(b[:len(b)/2])
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close() // the link breaks mid-download
			return
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(b))
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func checkManifest(version string) Manifest {
	return Manifest{ID: "demo", Name: Text{EN: "Demo"}, Version: version, Contract: 1,
		Platforms: map[string]Platform{runtime.GOOS + "/" + runtime.GOARCH: {Asset: "demo.tar.gz", SHA256: strings.Repeat("a", 64)}}}
}

// TestCheck_GitHubShapedRelease: today's GitHub release JSON and the
// download's redirect to the asset CDN with its signed query.
func TestCheck_GitHubShapedRelease(t *testing.T) {
	f := newRealGH(t, checkManifest("1.1.0"))
	r := &Resolver{API: f.srv.URL}
	rel, err := r.Check(context.Background(), Repo{"o", "r"}, false)
	if err != nil || rel.Manifest.Version != "1.1.0" || rel.Tag != "v1.1.0" {
		t.Fatalf("%+v %v", rel, err)
	}
	// the real CDN URL is an allowed redirect target
	if !r.allowedURL("https://release-assets.githubusercontent.com/github-production-release-asset/1/x?sp=r&sig=abc%3D", defaultAPI) {
		t.Fatal("release-assets.githubusercontent.com refused")
	}
}

// TestCheck_SlowManifestGoesOn: a caller that stops waiting gets
// ErrPending, the fetch finishes in the background and the next check
// finds it without asking GitHub again (the bug: a slow CDN path to the
// manifest ran past the list's deadline on every load, and nothing was
// ever kept).
func TestCheck_SlowManifestGoesOn(t *testing.T) {
	f := newRealGH(t, checkManifest("1.1.0"))
	f.maniDelay = 300 * time.Millisecond
	r := &Resolver{API: f.srv.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := r.Check(ctx, Repo{"o", "r"}, false); !errors.Is(err, ErrPending) {
		t.Fatalf("want ErrPending, got %v", err)
	}
	// a second caller meanwhile joins the same fetch
	if _, err := r.Check(ctx, Repo{"o", "r"}, false); !errors.Is(err, ErrPending) {
		t.Fatalf("want ErrPending, got %v", err)
	}
	rel, err := r.Check(context.Background(), Repo{"o", "r"}, false)
	if err != nil || rel.Manifest.Version != "1.1.0" {
		t.Fatalf("%+v %v", rel, err)
	}
	if _, err := r.Check(context.Background(), Repo{"o", "r"}, false); err != nil {
		t.Fatal(err)
	}
	if f.apiHits.Load() != 1 || f.maniHits.Load() != 1 {
		t.Fatalf("api %d, manifest %d hits: want one fetch", f.apiHits.Load(), f.maniHits.Load())
	}
}

func TestCheck_RetriesThenKeepsFailure(t *testing.T) {
	f := newRealGH(t, checkManifest("1.1.0"))
	f.maniFail.Store(1)
	r := &Resolver{API: f.srv.URL, wait: time.Millisecond}
	if _, err := r.Check(context.Background(), Repo{"o", "r"}, false); err != nil {
		t.Fatalf("a 503 then 200 is retried: %v", err)
	}

	f2 := newRealGH(t, checkManifest("1.1.0"))
	f2.maniFail.Store(100)
	r2 := &Resolver{API: f2.srv.URL, wait: time.Millisecond}
	_, err := r2.Check(context.Background(), Repo{"o", "r"}, false)
	if msg, ok := UserMessage(err); !ok || msg != "GitHub ответил с ошибкой 503" {
		t.Fatalf("%q %v", msg, err)
	}
	if f2.maniHits.Load() != fetchTries {
		t.Fatalf("%d manifest hits, want %d", f2.maniHits.Load(), fetchTries)
	}
	// the failure is kept a while: the polling pages do not ask again…
	r2.Check(context.Background(), Repo{"o", "r"}, false)
	if f2.apiHits.Load() != fetchTries {
		t.Fatalf("failure not kept: %d api hits", f2.apiHits.Load())
	}
	// …but «Проверить снова» does
	f2.maniFail.Store(0)
	if _, err := r2.Check(context.Background(), Repo{"o", "r"}, true); err != nil {
		t.Fatal(err)
	}
}

func TestCheck_RateLimit(t *testing.T) {
	f := newRealGH(t, checkManifest("1.1.0"))
	f.apiStatus = http.StatusForbidden
	f.apiHeader = http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {fmt.Sprint(time.Now().Add(20 * time.Minute).Unix())}}
	r := &Resolver{API: f.srv.URL}
	_, err := r.Check(context.Background(), Repo{"o", "r"}, false)
	msg, _ := UserMessage(err)
	if !strings.HasPrefix(msg, "GitHub ограничил число запросов") || !strings.Contains(msg, "через 20 мин") {
		t.Fatalf("%q", msg)
	}
	// not even a fresh check spends a request before the reset
	r.Check(context.Background(), Repo{"o", "r"}, true)
	if f.apiHits.Load() != 1 {
		t.Fatalf("%d api hits under the rate limit", f.apiHits.Load())
	}
}

func TestCheck_NewerContractNeedsCore(t *testing.T) {
	f := newRealGH(t, checkManifest("2.0.0"))
	var m map[string]any
	json.Unmarshal(f.manifest, &m)
	m["contract"] = 2
	m["minCore"] = "9.0.0"
	m["hooks"] = []string{"some.future"}
	f.manifest, _ = json.Marshal(m)
	_, err := (&Resolver{API: f.srv.URL}).Check(context.Background(), Repo{"o", "r"}, false)
	var nc NeedsCoreError
	if !errors.As(err, &nc) || nc.Version != "2.0.0" {
		t.Fatalf("%v", err)
	}
	if msg, ok := UserMessage(err); !ok || !strings.Contains(msg, "нужен более новый сервер") {
		t.Fatalf("%q", msg)
	}
	// a broken manifest is not a newer one
	f.manifest = []byte(`{"id":"demo","version":"x"}`)
	_, err = (&Resolver{API: f.srv.URL}).Check(context.Background(), Repo{"o", "r"}, false)
	if errors.As(err, &nc) || err == nil {
		t.Fatalf("%v", err)
	}
}

// TestFetch_ResumesBrokenDownload: a link that breaks mid-archive resumes
// with a Range request instead of failing the update.
func TestFetch_ResumesBrokenDownload(t *testing.T) {
	blob := bytes.Repeat([]byte("0123456789"), 50000)
	f := newRealGH(t, checkManifest("1.1.0"))
	f.blobs["demo.tar.gz"] = blob
	f.cut.Store(1)
	r := &Resolver{API: f.srv.URL, wait: time.Millisecond}
	rel, err := r.Check(context.Background(), Repo{"o", "r"}, false)
	if err != nil {
		t.Fatal(err)
	}
	tmp, err := os.CreateTemp(t.TempDir(), "dl")
	if err != nil {
		t.Fatal(err)
	}
	defer tmp.Close()
	if err := r.download(context.Background(), rel.assets["demo.tar.gz"], tmp); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(tmp.Name())
	if !bytes.Equal(got, blob) {
		t.Fatalf("got %d bytes, want %d", len(got), len(blob))
	}
	if f.cut.Load() != 0 {
		t.Fatal("the download never broke")
	}
}

// TestManager_CheckUpdateOldSource: an addon installed by an older core
// (its source stored as github.com/o/r, version 1.0.0) sees the newer
// release, and a failing check says why instead of «up to date».
func TestManager_CheckUpdateOldSource(t *testing.T) {
	m, _ := newManager(t)
	ctx := context.Background()
	f := newRealGH(t, checkManifest("1.1.0"))
	m.Resolver = &Resolver{API: f.srv.URL, wait: time.Millisecond}
	for _, src := range []string{"github.com/o/r", "https://github.com/o/r"} {
		in := Installed{ID: "demo", Source: src, Version: "1.0.0", Manifest: checkManifest("1.0.0"), Enabled: true,
			InstalledAt: time.Now(), UpdatedAt: time.Now()}
		if err := m.Store.Put(ctx, in, "tok"); err != nil {
			t.Fatal(err)
		}
		if c := m.CheckUpdate(ctx, "demo", false); c.Version != "1.1.0" || c.Err != nil {
			t.Fatalf("%s: %+v", src, c)
		}
	}

	f.apiStatus = http.StatusInternalServerError
	c := m.CheckUpdate(ctx, "demo", true)
	if c.Version != "" || c.Err == nil {
		t.Fatalf("failed check: %+v", c)
	}
	if msg, _ := UserMessage(c.Err); msg != "GitHub ответил с ошибкой 500" {
		t.Fatalf("%q", msg)
	}

	// still running past the caller's wait: pending, not «up to date»
	f.apiStatus = 0
	f.maniDelay = 300 * time.Millisecond
	short, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel()
	if c := m.CheckUpdate(short, "demo", true); !c.Pending {
		t.Fatalf("slow check: %+v", c)
	}
	time.Sleep(400 * time.Millisecond)
	if c := m.CheckUpdate(ctx, "demo", false); c.Version != "1.1.0" {
		t.Fatalf("after the background fetch: %+v", c)
	}
}
