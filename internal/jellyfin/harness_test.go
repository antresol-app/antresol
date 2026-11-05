package jellyfin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
)

const (
	homeAddr   = "192.168.1.20:50000"
	remoteAddr = "203.0.113.7:50000"
	proxyAddr  = "10.0.0.2:41000"

	anyaPassword = "anya-secret"
)

// syncBuf is a log sink safe for -race.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// env: a database with three profiles — «Аня» (adult admin, login
// password and app password), «Миша» (kid, MaxRating 6+), «Гость» (no
// login password, app password) — and a Server on it.
type env struct {
	t        *testing.T
	db       *sql.DB
	s        *Server
	users    *auth.Store
	tokens   *auth.AppTokens
	network  *auth.Network
	throttle *auth.Throttle
	logs     *syncBuf

	anya, misha, guest auth.User
	anyaApp, guestApp  string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	e := &env{t: t, db: d, users: auth.NewStore(d), tokens: auth.NewAppTokens(d), throttle: auth.NewThrottle(), logs: &syncBuf{},
		network: &auth.Network{Proxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")}}}
	if e.anya, err = e.users.CreateFirstAdmin(ctx, "Аня", anyaPassword); err != nil {
		t.Fatal(err)
	}
	if e.misha, err = e.users.CreateUser(ctx, auth.NewUser{Name: "Миша", Password: "misha-pw", IsKid: true, MaxRating: "6+"}); err != nil {
		t.Fatal(err)
	}
	if e.guest, err = e.users.CreateUser(ctx, auth.NewUser{Name: "Гость"}); err != nil {
		t.Fatal(err)
	}
	if e.anyaApp, err = e.users.SetAppPassword(ctx, e.anya.ID); err != nil {
		t.Fatal(err)
	}
	if e.guestApp, err = e.users.SetAppPassword(ctx, e.guest.ID); err != nil {
		t.Fatal(err)
	}
	e.s = e.server()
	return e
}

// server is a new Server on the env's database and shared helpers.
func (e *env) server() *Server {
	return &Server{Users: e.users, Tokens: e.tokens, Network: e.network, Throttle: e.throttle, DB: e.db,
		Version: "12.1.0", Log: slog.New(slog.NewTextHandler(e.logs, nil))}
}

// do sends a request from the home network. body: nil, a string (sent
// as is) or a value (JSON).
func (e *env) do(method, path string, hdr map[string]string, body any) *httptest.ResponseRecorder {
	return e.from(homeAddr, method, path, hdr, body)
}

// remote sends the request from an internet address.
func (e *env) remote(method, path string, hdr map[string]string, body any) *httptest.ResponseRecorder {
	return e.from(remoteAddr, method, path, hdr, body)
}

func (e *env) from(addr, method, path string, hdr map[string]string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(buf)
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = addr
	if rd != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.s.ServeHTTP(w, r)
	return w
}

// swiftfinHdr is the jellyfin-sdk-swift header (unquoted, Token after login).
func swiftfinHdr(token string) map[string]string {
	v := "MediaBrowser DeviceId=3F2B-iphone, Device=iPhone, Client=Swiftfin iOS, Version=1.3.0"
	if token != "" {
		v += ", Token=" + token
	}
	return map[string]string{"Authorization": v}
}

// login signs name in with pw from home and returns the access token.
func (e *env) login(name, pw string) string {
	e.t.Helper()
	w := e.do("POST", "/Users/AuthenticateByName", swiftfinHdr(""), map[string]string{"Username": name, "Pw": pw})
	if w.Code != 200 {
		e.t.Fatalf("login %s: %d %s", name, w.Code, w.Body)
	}
	var out struct{ AccessToken string }
	decode(e.t, w, &out)
	return out.AccessToken
}

func decode(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), v); err != nil {
		t.Fatalf("decode %q: %v", w.Body, err)
	}
}
