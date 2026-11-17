// Package addonkittest helps an addon's tests: ReplayGolden serves the
// core API's recorded answers (addonkit/testdata/golden), so an adapter
// over addonkit.Client can be tested against what the core really says
// without the core. It is a package of its own so that addon binaries,
// which import addonkit, never link package testing. Standard library only.
package addonkittest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
	"time"
)

// Golden is one recorded answer of the core API: the request's method and
// path (with its query, as the client sends it, under /api/addon-core/v1),
// the answer's status and JSON body. Paths below the server's temporary
// library read {root}; timestamps read {time}.
type Golden struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
}

// GoldenTime is what ReplayGolden answers for a recorded timestamp: the
// files read "{time}" (a JSON string of its own), which no time.Time
// decodes.
var GoldenTime = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// goldenTimeJSON: the files' timestamp placeholder and its answer.
var goldenTimeJSON = []byte(`"{time}"`)

// GoldenDir is addonkit's own testdata/golden (beside this package's
// source), where the core records its answers.
func GoldenDir() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return filepath.Join(filepath.Dir(file), "..", "testdata", "golden")
}

// LoadGoldens reads every *.json of dir, by file name (without .json).
func LoadGoldens(t testing.TB, dir string) map[string]Golden {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatalf("addonkittest: no golden answers in %s", dir)
	}
	sort.Strings(names)
	out := make(map[string]Golden, len(names))
	for _, n := range names {
		b, err := os.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		var g Golden
		if err := json.Unmarshal(b, &g); err != nil {
			t.Fatalf("addonkittest: %s: %v", n, err)
		}
		if g.Method == "" || g.Path == "" || g.Status == 0 {
			t.Fatalf("addonkittest: %s: method, path and status are required", n)
		}
		out[filepath.Base(n[:len(n)-len(".json")])] = g
	}
	return out
}

type route struct {
	method, path string
	query        url.Values
	g            Golden
}

// ReplayGolden serves the golden answers of dir: a request whose method,
// path and query (compared decoded, in any order) are a file's gets that
// file's status and body; anything else is 404 {"error"}. Two files with
// the same request fail the test. A "{time}" string in a body is answered
// as GoldenTime (RFC 3339). The server is closed with the test.
// Point an addonkit.Client at it with Env{CoreURL: srv.URL}.
func ReplayGolden(t testing.TB, dir string) *httptest.Server {
	t.Helper()
	var routes []route
	for name, g := range LoadGoldens(t, dir) {
		u, err := url.Parse(g.Path)
		if err != nil {
			t.Fatalf("addonkittest: %s: path: %v", name, err)
		}
		r := route{method: g.Method, path: u.EscapedPath(), query: u.Query(), g: g}
		for _, o := range routes {
			if o.method == r.method && o.path == r.path && reflect.DeepEqual(o.query, r.query) {
				t.Fatalf("addonkittest: %s: the same request as another golden answer", name)
			}
		}
		routes = append(routes, r)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		q := r.URL.Query()
		for _, rt := range routes {
			if rt.method != r.Method || rt.path != r.URL.EscapedPath() || !reflect.DeepEqual(rt.query, q) {
				continue
			}
			body := bytes.ReplaceAll(bytes.TrimSpace(rt.g.Body), goldenTimeJSON,
				[]byte(`"`+GoldenTime.Format(time.RFC3339)+`"`))
			if len(body) > 0 {
				w.Header().Set("Content-Type", "application/json")
			}
			w.WriteHeader(rt.g.Status)
			w.Write(body)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"error":"no golden answer"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}
