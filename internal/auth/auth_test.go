package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

func newStores(t *testing.T) (*Store, *Sessions) {
	s, ss, _ := newStoresDB(t)
	return s, ss
}

func newStoresDB(t *testing.T) (*Store, *Sessions, *sql.DB) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return NewStore(d), NewSessions(d), d
}

func TestStoreBasics(t *testing.T) {
	ctx := context.Background()
	s, _ := newStores(t)
	if need, _ := s.NeedsSetup(ctx); !need {
		t.Fatal("expected needs setup")
	}
	if _, err := s.CreateUser(ctx, NewUser{Name: "A", IsAdmin: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("admin without password: %v", err)
	}
	a, err := s.CreateFirstAdmin(ctx, "Papa", "secret")
	if err != nil || !a.HasPassword || a.Avatar != "grad-1" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := s.CreateFirstAdmin(ctx, "X", "secret"); !errors.Is(err, ErrSetupDone) {
		t.Fatal(err)
	}
	k, _ := s.CreateUser(ctx, NewUser{Name: "Kid", IsKid: true, MaxRating: "PG"})
	if k.Avatar != "grad-2" || k.HasPassword || k.MaxRating == nil {
		t.Fatalf("%+v", k)
	}
	if _, err := s.CreateUser(ctx, NewUser{Name: "Kid"}); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, a.ID, "nope"); !errors.Is(err, ErrBadPassword) {
		t.Fatal(err)
	}
	if _, err := s.Verify(ctx, a.ID, "secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, a.ID); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("delete last admin: %v", err)
	}
	empty := ""
	if err := s.Update(ctx, a.ID, UserPatch{Password: &empty}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("admin password removal: %v", err)
	}
	if err := s.Delete(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
}

func TestSessions(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	u, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	tok, err := ss.Create(ctx, u.ID)
	if err != nil || len(tok) != 43 {
		t.Fatalf("%q %v", tok, err)
	}
	got, err := ss.Lookup(ctx, tok)
	if err != nil || got.ID != u.ID {
		t.Fatal(got, err)
	}
	ss.Revoke(ctx, tok)
	if _, err := ss.Lookup(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestMiddleware(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	u, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	k, _ := s.CreateUser(ctx, NewUser{Name: "Kid", IsKid: true})
	h := Require(ss)(RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	do := func(uid int64) int {
		r := httptest.NewRequest("GET", "/", nil)
		if uid != 0 {
			tok, _ := ss.Create(ctx, uid)
			r.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if c := do(0); c != 401 {
		t.Fatal(c)
	}
	if c := do(k.ID); c != 403 {
		t.Fatal(c)
	}
	if c := do(u.ID); c != 204 {
		t.Fatal(c)
	}
}

func TestPasswordTooLong(t *testing.T) {
	s, _ := newStores(t)
	_, err := s.CreateFirstAdmin(context.Background(), "A", strings.Repeat("x", 73))
	if !errors.Is(err, ErrPasswordTooLong) || !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestExpiredSession(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	u, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	tok, _ := ss.Create(ctx, u.ID)
	old := time.Now().Add(-sessionTTL - time.Hour).UTC().Format(time.RFC3339)
	d.Exec(`UPDATE sessions SET last_seen=? WHERE token=?`, old, TokenHash(tok))
	if _, err := ss.Lookup(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	var n int
	d.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token=?`, TokenHash(tok)).Scan(&n)
	if n != 0 {
		t.Fatal("expired session not revoked")
	}
}

func TestLastSeenThrottle(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	u, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	tok, _ := ss.Create(ctx, u.ID)
	seen := func() string {
		var v string
		d.QueryRow(`SELECT last_seen FROM sessions WHERE token=?`, TokenHash(tok)).Scan(&v)
		return v
	}
	recent := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	d.Exec(`UPDATE sessions SET last_seen=? WHERE token=?`, recent, TokenHash(tok))
	ss.Lookup(ctx, tok)
	if seen() != recent {
		t.Fatal("last_seen written within throttle window")
	}
	stale := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	d.Exec(`UPDATE sessions SET last_seen=? WHERE token=?`, stale, TokenHash(tok))
	ss.Lookup(ctx, tok)
	ss.seen.wait()
	if seen() == stale {
		t.Fatal("last_seen not refreshed")
	}
}

// holdWriteLock holds the database's write lock (a commit stalled on a
// busy disk) until the returned func is called.
func holdWriteLock(t *testing.T, d *sql.DB) func() {
	t.Helper()
	tx, err := d.Begin() // BEGIN IMMEDIATE (_txlock=immediate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE settings SET value=value`); err != nil {
		t.Fatal(err)
	}
	return func() { tx.Commit() }
}

// A request never waits for the write lock to refresh last_seen: the
// lookup answers at once, the write lands once the lock is free.
func TestLastSeenNeverWaitsForWriteLock(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	u, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	tok, _ := ss.Create(ctx, u.ID)
	stale := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	d.Exec(`UPDATE sessions SET last_seen=? WHERE token=?`, stale, TokenHash(tok))
	release := holdWriteLock(t, d)
	t0 := time.Now()
	got, _, err := ss.LookupSession(ctx, tok)
	if err != nil || got.ID != u.ID {
		t.Fatalf("lookup under the write lock: %+v %v", got, err)
	}
	if el := time.Since(t0); el > time.Second {
		t.Fatalf("lookup waited %v for the write lock", el)
	}
	time.Sleep(100 * time.Millisecond)
	release()
	ss.seen.wait()
	var v string
	d.QueryRow(`SELECT last_seen FROM sessions WHERE token=?`, TokenHash(tok)).Scan(&v)
	if v == stale {
		t.Fatal("last_seen not refreshed after the lock was released")
	}
}

func TestMiddlewareDBErrorIs500(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	u, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	tok, _ := ss.Create(ctx, u.ID)
	d.Close()
	h := Require(ss)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 || !strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestByNameCyrillicCase(t *testing.T) {
	ctx := context.Background()
	s, _ := newStores(t)
	u, err := s.CreateUser(ctx, NewUser{Name: "Саша"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.ByName(ctx, " саша ")
	if err != nil || got.ID != u.ID {
		t.Fatalf("ByName(саша) = %+v, %v", got, err)
	}
	if _, err := s.ByName(ctx, "Маша"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown name: %v", err)
	}
	if _, err := s.CreateUser(ctx, NewUser{Name: "саша"}); err != nil {
		t.Fatal(err) // names are unique byte for byte only
	}
	if _, err := s.ByName(ctx, "САША"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("two profiles match: want ErrNotFound, got %v", err)
	}
}

func TestAppPasswordStore(t *testing.T) {
	ctx := context.Background()
	s, _ := newStores(t)
	u, _ := s.CreateUser(ctx, NewUser{Name: "Кира"})
	if _, ok, err := s.AppPassword(ctx, u.ID); ok || err != nil {
		t.Fatalf("new profile has an app password: %v %v", ok, err)
	}
	p1, err := s.SetAppPassword(ctx, u.ID)
	if err != nil || !appPasswordRe.MatchString(p1) {
		t.Fatalf("%q %v", p1, err)
	}
	p2, _ := s.SetAppPassword(ctx, u.ID)
	if p2 == p1 {
		t.Fatal("a new app password must differ")
	}
	if got, ok, _ := s.AppPassword(ctx, u.ID); !ok || got != p2 {
		t.Fatalf("AppPassword = %q %v", got, ok)
	}
	// the info for the UI: whether one is set and since when, never the value
	if ok, at, err := s.AppPasswordInfo(ctx, u.ID); err != nil || !ok || time.Since(at) > time.Minute || at.After(time.Now().Add(time.Second)) {
		t.Fatalf("AppPasswordInfo = %v %v %v", ok, at, err)
	}
	if err := s.ClearAppPassword(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.AppPassword(ctx, u.ID); ok {
		t.Fatal("cleared")
	}
	if ok, at, err := s.AppPasswordInfo(ctx, u.ID); err != nil || ok || !at.IsZero() {
		t.Fatalf("AppPasswordInfo after clear = %v %v %v", ok, at, err)
	}
	if _, _, err := s.AppPasswordInfo(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.SetAppPassword(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

var appPasswordRe = regexp.MustCompile(`^[a-z0-9]{4}(-[a-z0-9]{4}){3}$`)
