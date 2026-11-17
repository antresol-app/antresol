package addons

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseRepoURL(t *testing.T) {
	good := map[string]Repo{
		"https://github.com/o/r":      {"o", "r"},
		"https://github.com/o/r.git":  {"o", "r"},
		"https://github.com/o/r/":     {"o", "r"},
		"github.com/o/r":              {"o", "r"},
		"https://github.com/o-x/r.y_": {"o-x", "r.y_"},
	}
	for in, want := range good {
		got, err := ParseRepoURL(in)
		if err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, in := range []string{"http://github.com/o/r", "https://gitlab.com/o/r", "github.com/o",
		"https://github.com/o/r/tree/main", "https://github.com/o/r/extra", "", "https://github.com/o/r?x=1", "https://github.com//r"} {
		if _, err := ParseRepoURL(in); err == nil {
			t.Errorf("%q: want error", in)
		}
	}
}

func sum(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

type fakeGH struct {
	srv      *httptest.Server
	hits     atomic.Int32
	manifest []byte
	assets   map[string][]byte
	noMani   bool
	redirect string // set: every /dl/ request redirects here
}

func newFakeGH(t *testing.T, m Manifest, assets map[string][]byte) *fakeGH {
	f := &fakeGH{assets: assets}
	f.manifest, _ = json.Marshal(m)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		f.hits.Add(1)
		type a struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}
		var as []a
		if !f.noMani {
			as = append(as, a{ManifestName, f.srv.URL + "/dl/" + ManifestName})
		}
		for n := range f.assets {
			as = append(as, a{n, f.srv.URL + "/dl/" + n})
		}
		json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.0.0", "assets": as})
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, r *http.Request) {
		n := strings.TrimPrefix(r.URL.Path, "/dl/")
		if f.redirect != "" {
			http.Redirect(w, r, f.redirect+"/dl/"+n, http.StatusFound)
			return
		}
		if n == ManifestName {
			w.Write(f.manifest)
			return
		}
		if b, ok := f.assets[n]; ok {
			w.Write(b)
			return
		}
		http.NotFound(w, r)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func testManifest(webSum string) Manifest {
	return Manifest{ID: "demo", Name: Text{EN: "Demo"}, Version: "1.0.0", Contract: 1,
		Web: &Platform{Asset: "web.tar.gz", SHA256: webSum}}
}

func TestLatest_ReadsManifest(t *testing.T) {
	f := newFakeGH(t, testManifest(strings.Repeat("a", 64)), map[string][]byte{"web.tar.gz": []byte("x")})
	r := &Resolver{API: f.srv.URL}
	rel, err := r.Latest(context.Background(), Repo{"o", "r"})
	if err != nil {
		t.Fatal(err)
	}
	if rel.Tag != "v1.0.0" || rel.Manifest.ID != "demo" {
		t.Fatalf("%+v", rel)
	}
	if _, err := r.Latest(context.Background(), Repo{"o", "r"}); err != nil {
		t.Fatal(err)
	}
	if f.hits.Load() != 1 {
		t.Fatalf("cache miss: %d hits", f.hits.Load())
	}
}

// TestLatest_RedirectToOtherHostRefused: a redirect leaves the allowed
// hosts no more than an asset URL may.
func TestLatest_RedirectToOtherHostRefused(t *testing.T) {
	other := newFakeGH(t, testManifest(strings.Repeat("a", 64)), nil)
	var otherHits atomic.Int32
	other.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits.Add(1)
		w.Write(other.manifest)
	})
	f := newFakeGH(t, testManifest(strings.Repeat("a", 64)), nil)
	f.redirect = other.srv.URL
	_, err := (&Resolver{API: f.srv.URL}).Latest(context.Background(), Repo{"o", "r"})
	if err == nil {
		t.Fatal("redirect to another host followed")
	}
	if otherHits.Load() != 0 {
		t.Fatalf("other host reached %d times", otherHits.Load())
	}
	// a redirect within the allowed hosts is followed
	f.redirect = f.srv.URL + "/dl"
	f.srv.Config.Handler.(*http.ServeMux).HandleFunc("/dl/dl/", func(w http.ResponseWriter, _ *http.Request) { w.Write(f.manifest) })
	if _, err := (&Resolver{API: f.srv.URL}).Latest(context.Background(), Repo{"o", "r"}); err != nil {
		t.Fatalf("same-host redirect: %v", err)
	}
}

func TestAllowedURLHosts(t *testing.T) {
	r := &Resolver{}
	for raw, want := range map[string]bool{
		"https://github.com/o/r/releases/download/v1/a":  true,
		"https://objects.githubusercontent.com/x":        true,
		"https://release-assets.githubusercontent.com/x": true,
		"http://github.com/o/r":                          false,
		"https://example.org/x":                          false,
		"https://user@github.com/x":                      false,
		"https://github.com.example.org/x":               false,
	} {
		if got := r.allowedURL(raw, defaultAPI); got != want {
			t.Errorf("%s: %v, want %v", raw, got, want)
		}
	}
}

func TestLatest_NoManifestAsset(t *testing.T) {
	f := newFakeGH(t, testManifest(strings.Repeat("a", 64)), nil)
	f.noMani = true
	_, err := (&Resolver{API: f.srv.URL}).Latest(context.Background(), Repo{"o", "r"})
	if err == nil || err.Error() != "В релизе нет antresol-addon.json" {
		t.Fatalf("%v", err)
	}
}

func TestFetch_OK_and_ChecksumMismatch(t *testing.T) {
	arch := makeTGZ(t, []tent{{name: "index.html", typ: tar.TypeReg, mode: 0o644, body: "hi"}})
	// upper-case hex must be accepted
	f := newFakeGH(t, testManifest(strings.ToUpper(sum(arch))), map[string][]byte{"web.tar.gz": arch})
	r := &Resolver{API: f.srv.URL}
	rel, err := r.Latest(context.Background(), Repo{"o", "r"})
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	dst := filepath.Join(base, "web")
	if err := r.Fetch(context.Background(), rel, *rel.Manifest.Web, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "index.html")); string(b) != "hi" {
		t.Fatalf("%q", b)
	}

	bad := *rel.Manifest.Web
	bad.SHA256 = strings.Repeat("0", 64)
	dst2 := filepath.Join(base, "web2")
	if err := r.Fetch(context.Background(), rel, bad, dst2); err == nil {
		t.Fatal("want mismatch error")
	}
	left, _ := os.ReadDir(base)
	if len(left) != 1 {
		t.Fatalf("leftovers: %v", left)
	}
}

func TestFetch_UnknownAsset(t *testing.T) {
	r := &Resolver{}
	err := r.Fetch(context.Background(), Release{}, Platform{Asset: "nope", SHA256: strings.Repeat("0", 64)}, filepath.Join(t.TempDir(), "d"))
	if err == nil {
		t.Fatal("want error")
	}
}

func TestLocalRelease(t *testing.T) {
	dir := t.TempDir()
	arch := makeTGZ(t, []tent{{name: "a.txt", typ: tar.TypeReg, mode: 0o644, body: "a"}})
	m, _ := json.Marshal(testManifest(sum(arch)))
	os.WriteFile(filepath.Join(dir, ManifestName), m, 0o644)
	os.WriteFile(filepath.Join(dir, "web.tar.gz"), arch, 0o644)
	rel, err := LocalRelease(dir)
	if err != nil || rel.Manifest.ID != "demo" {
		t.Fatalf("%v %+v", err, rel)
	}
	dst := filepath.Join(t.TempDir(), "o")
	if err := (&Resolver{}).Fetch(context.Background(), rel, *rel.Manifest.Web, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "a.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := LocalRelease(t.TempDir()); err == nil {
		t.Fatal("want error")
	}
	_ = fmt.Sprint
}

func TestLatest_RefusesFileURL(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(secret, []byte("{}"), 0o644)
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases/latest", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"tag_name":"v1","assets":[{"name":%q,"browser_download_url":"file://%s"}]}`, ManifestName, secret)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if _, err := (&Resolver{API: srv.URL}).Latest(context.Background(), Repo{"o", "r"}); err == nil {
		t.Fatal("want error")
	}
}
