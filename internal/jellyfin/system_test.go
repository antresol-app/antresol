package jellyfin

import (
	"regexp"
	"testing"
)

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

type publicInfo struct {
	LocalAddress, ServerName, Version, ProductName, OperatingSystem, Id string
	StartupWizardCompleted                                              bool
}

func TestPublicInfo(t *testing.T) {
	e := newEnv(t)
	w := e.remote("GET", "/System/Info/Public", nil, nil)
	if w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	var p publicInfo
	decode(t, w, &p)
	if p.ServerName != "Antresol" || p.Version != "12.1.0" || p.ProductName != "Jellyfin Server" || p.OperatingSystem != "Linux" ||
		!hex32.MatchString(p.Id) || !p.StartupWizardCompleted || p.LocalAddress != "http://example.com" {
		t.Fatalf("%+v", p)
	}
	from := func(addr string) string {
		r := map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "198.51.100.4", "Host": "x"}
		w := e.from(addr, "GET", "/system/info/public", r, nil)
		var p publicInfo
		decode(t, w, &p)
		return p.LocalAddress
	}
	if got := from(proxyAddr); got != "https://example.com" {
		t.Fatalf("from the proxy: %q", got)
	}
	if got := from(remoteAddr); got != "http://example.com" {
		t.Fatalf("X-Forwarded-Proto from a stranger: %q", got)
	}
	// System/Info needs a token
	if w := e.do("GET", "/System/Info", nil, nil); w.Code != 401 || w.Body.Len() != 0 {
		t.Fatalf("system/info without token: %d %q", w.Code, w.Body)
	}
	tok := e.login("Аня", e.anyaApp)
	w = e.do("GET", "/System/Info", swiftfinHdr(tok), nil)
	var full struct {
		publicInfo
		HasPendingRestart, IsShuttingDown, CanSelfRestart, SupportsLibraryMonitor *bool
	}
	decode(t, w, &full)
	if w.Code != 200 || full.Id != p.Id || full.HasPendingRestart == nil || *full.HasPendingRestart || full.CanSelfRestart == nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, m := range []string{"GET", "POST"} {
		if w := e.remote(m, "/System/Ping", nil, nil); w.Code != 200 || w.Body.String() != `"Jellyfin Server"` {
			t.Fatalf("ping %s: %d %s", m, w.Code, w.Body)
		}
	}
	if w := e.remote("GET", "/Branding/Configuration", nil, nil); w.Code != 200 || w.Body.String() != `{"LoginDisclaimer":"","CustomCss":"","SplashscreenEnabled":false}` {
		t.Fatalf("branding: %d %s", w.Code, w.Body)
	}
	if w := e.remote("GET", "/Branding/Css", nil, nil); w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("css: %d %q %q", w.Code, w.Body, w.Header().Get("Content-Type"))
	}
	if w := e.remote("GET", "/QuickConnect/Enabled", nil, nil); w.Code != 200 || w.Body.String() != "false" {
		t.Fatalf("quickconnect: %d %s", w.Code, w.Body)
	}
}

func TestServerIDStable(t *testing.T) {
	e := newEnv(t)
	id := func(s *Server) string {
		e.s = s
		var p publicInfo
		decode(t, e.do("GET", "/System/Info/Public", nil, nil), &p)
		return p.Id
	}
	first := id(e.s)
	second := id(e.server()) // a new Server (a restart) on the same database
	if first == "" || first != second {
		t.Fatalf("%q vs %q", first, second)
	}
	w := e.do("POST", "/Users/AuthenticateByName", swiftfinHdr(""), map[string]string{"Username": "Аня", "Pw": e.anyaApp})
	var res struct {
		ServerId string
		User     struct{ ServerId string }
	}
	decode(t, w, &res)
	if res.ServerId != first || res.User.ServerId != first {
		t.Fatalf("login: %s", w.Body)
	}
}
