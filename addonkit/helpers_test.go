package addonkit

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func goodHelper() Helper {
	return Helper{Name: "cache-svc", Image: "ghcr.io/example/cache-svc:1.2.3", Port: 8191, Health: "/"}
}

func TestHelperValid(t *testing.T) {
	if err := goodHelper().Valid(); err != nil {
		t.Fatalf("good helper: %v", err)
	}
	bad := map[string]func(h *Helper){
		"empty name":       func(h *Helper) { h.Name = "" },
		"upper name":       func(h *Helper) { h.Name = "Cache" },
		"dot name":         func(h *Helper) { h.Name = "cache.svc" },
		"trailing dash":    func(h *Helper) { h.Name = "cache-" },
		"long name":        func(h *Helper) { h.Name = "a" + strings.Repeat("b", 63) },
		"digit first":      func(h *Helper) { h.Name = "1cache" },
		"no image":         func(h *Helper) { h.Image = "" },
		"space in image":   func(h *Helper) { h.Image = "ghcr.io/x y" },
		"image newline":    func(h *Helper) { h.Image = "ghcr.io/x\nports: [1:1]" },
		"port zero":        func(h *Helper) { h.Port = 0 },
		"port high":        func(h *Helper) { h.Port = 65536 },
		"no health":        func(h *Helper) { h.Health = "" },
		"relative health":  func(h *Helper) { h.Health = "health" },
		"scheme health":    func(h *Helper) { h.Health = "//evil.example/" },
		"dotdot health":    func(h *Helper) { h.Health = "/a/../b" },
		"query health":     func(h *Helper) { h.Health = "/?uid=x" },
		"space in health":  func(h *Helper) { h.Health = "/a b" },
		"backslash health": func(h *Helper) { h.Health = `/a\b` },
	}
	for name, mod := range bad {
		t.Run(name, func(t *testing.T) {
			h := goodHelper()
			mod(&h)
			if h.Valid() == nil {
				t.Fatalf("%+v: want an error", h)
			}
		})
	}
}

func TestHelperAddress(t *testing.T) {
	if got := goodHelper().Address(); got != "http://cache-svc:8191" {
		t.Fatalf("address %q", got)
	}
}

func TestHelperComposeBlock(t *testing.T) {
	got := goodHelper().Compose()
	want := "  cache-svc:\n" +
		"    image: ghcr.io/example/cache-svc:1.2.3\n" +
		"    restart: unless-stopped\n"
	if got != want {
		t.Fatalf("compose block:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "ports") {
		t.Fatal("a helper never publishes a port")
	}
}

func TestHelpersFromManifest(t *testing.T) {
	raw := []byte(`{"id":"demo","version":"1.0.0","contract":1,"platforms":{},
		"helpers":[{"name":"cache-svc","image":"ghcr.io/example/cache-svc:1.2.3","port":8191,"health":"/"}]}`)
	hs, err := HelpersFromManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(hs) != 1 || hs[0] != goodHelper() {
		t.Fatalf("helpers %+v", hs)
	}
	// a manifest without the field: none, no error (contract 1 as before)
	hs, err = HelpersFromManifest([]byte(`{"id":"demo","contract":1}`))
	if err != nil || len(hs) != 0 {
		t.Fatalf("no field: %+v %v", hs, err)
	}
	// an invalid entry is an error, as the server would refuse it
	if _, err := HelpersFromManifest([]byte(`{"helpers":[{"name":"X","image":"i","port":1,"health":"/"}]}`)); err == nil {
		t.Fatal("bad entry: want an error")
	}
	if _, err := HelpersFromManifest([]byte(`{"helpers":[` +
		`{"name":"a1","image":"i","port":1,"health":"/"},{"name":"a1","image":"i","port":2,"health":"/"}]}`)); err == nil {
		t.Fatal("repeated name: want an error")
	}
	if _, err := HelpersFromManifest([]byte(`{"helpers":[{"name":"a1","image":"i","port":1,"health":"/","extra":1}]}`)); err == nil {
		t.Fatal("unknown key: want an error")
	}
	if _, err := HelpersFromManifest([]byte(`not json`)); err == nil {
		t.Fatal("bad JSON: want an error")
	}
}

// helperOn is h pointed at srv (its host and port), as compose DNS would.
func helperOn(t *testing.T, srv *httptest.Server, health string) (Helper, *http.Client) {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	h := Helper{Name: "cache-svc", Image: "img", Port: 8191, Health: health}
	// the client dials srv whatever the host: the fake compose network
	tr := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, u.Host)
	}}
	return h, &http.Client{Transport: tr}
}

func TestProbeHelper(t *testing.T) {
	var gotHost, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotPath = r.Host, r.URL.Path
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte("ready"))
		case "/redirect":
			http.Redirect(w, r, "http://elsewhere.example/", http.StatusFound)
		default:
			http.Error(w, "no", http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	h, client := helperOn(t, srv, "/ok")
	if err := ProbeHelper(context.Background(), client, h); err != nil {
		t.Fatalf("up: %v", err)
	}
	if gotHost != "cache-svc:8191" || gotPath != "/ok" {
		t.Fatalf("asked %s %s", gotHost, gotPath)
	}
	// a redirect is an answer: up, and it is not followed
	h.Health = "/redirect"
	if err := ProbeHelper(context.Background(), client, h); err != nil {
		t.Fatalf("redirect: %v", err)
	}
	if gotPath != "/redirect" {
		t.Fatalf("followed the redirect to %s", gotPath)
	}
	h.Health = "/fail"
	if err := ProbeHelper(context.Background(), client, h); err == nil {
		t.Fatal("500: want down")
	}
	// nothing listening: down
	srv.Close()
	h.Health = "/ok"
	if err := ProbeHelper(context.Background(), client, h); err == nil {
		t.Fatal("closed: want down")
	}
}

func TestProbeHelperRefusesAnInvalidHelper(t *testing.T) {
	h := goodHelper()
	h.Health = "//evil.example/"
	if err := ProbeHelper(context.Background(), &http.Client{Transport: failTransport{t}}, h); err == nil {
		t.Fatal("want an error")
	}
}

type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(*http.Request) (*http.Response, error) {
	f.t.Fatal("an invalid helper must not be asked")
	return nil, errors.New("no")
}

func TestHelperWatchFollowsTheService(t *testing.T) {
	var mu sync.Mutex
	up := false
	probe := func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if up {
			return nil
		}
		return errors.New("down")
	}
	changes := make(chan bool, 8)
	w := &HelperWatch{Helper: goodHelper(), Probe: probe, Every: time.Millisecond,
		OnChange: func(found bool) { changes <- found }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if w.Found() {
		t.Fatal("found before any probe")
	}
	if got := w.Check(ctx); got {
		t.Fatal("first check: want not found")
	}
	go w.Run(ctx)
	mu.Lock()
	up = true
	mu.Unlock()
	select {
	case f := <-changes:
		if !f {
			t.Fatal("first change should be found")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never found")
	}
	if !w.Found() || w.Address() != "http://cache-svc:8191" {
		t.Fatalf("found %v address %q", w.Found(), w.Address())
	}
	mu.Lock()
	up = false
	mu.Unlock()
	select {
	case f := <-changes:
		if f {
			t.Fatal("want lost")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("never lost")
	}
	if w.Address() != "" {
		t.Fatalf("address while lost: %q", w.Address())
	}
}

func TestHelperStatus(t *testing.T) {
	w := &HelperWatch{Helper: goodHelper(), Probe: func(context.Context) error { return nil }}
	if st := w.Status(true); st.Source != HelperOverride || st.Address != "" || st.Compose != "" || st.Name != "cache-svc" {
		t.Fatalf("override: %+v", st)
	}
	if st := w.Status(false); st.Source != HelperNone || st.Compose != goodHelper().Compose() || st.Address != "http://cache-svc:8191" {
		t.Fatalf("not found yet: %+v", st)
	}
	w.Check(context.Background())
	if st := w.Status(false); st.Source != HelperCompose || st.Address != "http://cache-svc:8191" || st.Compose != "" {
		t.Fatalf("found: %+v", st)
	}
	var nilWatch *HelperWatch
	if st := nilWatch.Status(false); st.Source != HelperNone || st.Name != "" {
		t.Fatalf("nil watch: %+v", st)
	}
}
