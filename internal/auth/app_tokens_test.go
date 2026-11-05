package auth

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

func tokenEnv(t *testing.T) (*Store, *AppTokens, User) {
	t.Helper()
	s, _, d := newStoresDB(t)
	u, err := s.CreateFirstAdmin(context.Background(), "Аня", "secret-pass")
	if err != nil {
		t.Fatal(err)
	}
	return s, NewAppTokens(d), u
}

func TestAppTokensIssueLookupRevoke(t *testing.T) {
	ctx := context.Background()
	_, tk, u := tokenEnv(t)
	dev := Device{ID: "dev-1", Name: "Apple TV", Client: "Infuse-Direct", Version: "8.5.6"}
	tok, err := tk.Issue(ctx, u.ID, "jellyfin", dev)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(tok) {
		t.Fatalf("token %q", tok)
	}
	got, gd, err := tk.Lookup(ctx, tok)
	if err != nil || got.ID != u.ID || got.Name != "Аня" || gd != dev {
		t.Fatalf("lookup %+v %+v %v", got, gd, err)
	}
	if _, _, err := tk.Lookup(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty: %v", err)
	}
	if _, _, err := tk.Lookup(ctx, "0123456789abcdef0123456789abcdef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if err := tk.Revoke(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tk.Lookup(ctx, tok); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked: %v", err)
	}
}

func TestAppTokensSameDeviceReplaces(t *testing.T) {
	ctx := context.Background()
	_, tk, u := tokenEnv(t)
	a, _ := tk.Issue(ctx, u.ID, "jellyfin", Device{ID: "tv"})
	b, _ := tk.Issue(ctx, u.ID, "jellyfin", Device{ID: "tv"})
	if a == b {
		t.Fatal("same token twice")
	}
	if _, _, err := tk.Lookup(ctx, a); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token of the device must go: %v", err)
	}
	if _, _, err := tk.Lookup(ctx, b); err != nil {
		t.Fatal(err)
	}
	// another device, and devices without an id, keep their tokens
	c, _ := tk.Issue(ctx, u.ID, "jellyfin", Device{ID: "phone"})
	x, _ := tk.Issue(ctx, u.ID, "jellyfin", Device{})
	y, _ := tk.Issue(ctx, u.ID, "jellyfin", Device{})
	for _, tok := range []string{b, c, x, y} {
		if _, _, err := tk.Lookup(ctx, tok); err != nil {
			t.Fatalf("%s: %v", tok, err)
		}
	}
}

// An app's request never waits for the write lock to refresh last_seen
// (as with web sessions).
func TestAppTokenLastSeenNeverWaitsForWriteLock(t *testing.T) {
	ctx := context.Background()
	s, _, d := newStoresDB(t)
	u, _ := s.CreateFirstAdmin(ctx, "Аня", "secret-pass")
	tk := NewAppTokens(d)
	tok, err := tk.Issue(ctx, u.ID, "jellyfin", Device{ID: "tv"})
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	d.Exec(`UPDATE app_tokens SET last_seen=? WHERE token=?`, stale, tok)
	release := holdWriteLock(t, d)
	t0 := time.Now()
	if got, _, err := tk.Lookup(ctx, tok); err != nil || got.ID != u.ID {
		t.Fatalf("lookup under the write lock: %+v %v", got, err)
	}
	if el := time.Since(t0); el > time.Second {
		t.Fatalf("lookup waited %v for the write lock", el)
	}
	release()
	tk.seen.wait()
	var v string
	d.QueryRow(`SELECT last_seen FROM app_tokens WHERE token=?`, tok).Scan(&v)
	if v == stale {
		t.Fatal("last_seen not refreshed after the lock was released")
	}
}

func TestAppTokensRevokedOnAppPasswordChange(t *testing.T) {
	ctx := context.Background()
	s, tk, u := tokenEnv(t)
	issue := func() string {
		t.Helper()
		tok, err := tk.Issue(ctx, u.ID, "jellyfin", Device{ID: "tv"})
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	gone := func(what, tok string) {
		t.Helper()
		if _, _, err := tk.Lookup(ctx, tok); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%s: token still works (%v)", what, err)
		}
	}
	tok := issue()
	if _, err := s.SetAppPassword(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	gone("SetAppPassword", tok)

	tok = issue()
	if err := s.ClearAppPassword(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	gone("ClearAppPassword", tok)

	tok = issue()
	name := "Анна"
	if err := s.Update(ctx, u.ID, UserPatch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tk.Lookup(ctx, tok); err != nil {
		t.Fatalf("a rename keeps tokens: %v", err)
	}
	pw := "new-secret"
	if err := s.Update(ctx, u.ID, UserPatch{Password: &pw}); err != nil {
		t.Fatal(err)
	}
	gone("login password change", tok)

	k, err := s.CreateUser(ctx, NewUser{Name: "Миша", Password: "kidpass", IsKid: true})
	if err != nil {
		t.Fatal(err)
	}
	ktok, _ := tk.Issue(ctx, k.ID, "jellyfin", Device{})
	empty := ""
	if err := s.Update(ctx, k.ID, UserPatch{Password: &empty}); err != nil {
		t.Fatal(err)
	}
	gone("login password removed", ktok)

	ktok, _ = tk.Issue(ctx, k.ID, "jellyfin", Device{})
	if err := s.Delete(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	gone("Delete", ktok)
}

func TestMigration0018(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v.db")
	for i := 0; i < 2; i++ {
		d, err := db.Open(p)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='app_tokens'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table: %d %v", n, err)
		}
		if err := d.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='app_tokens_user'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("index: %d %v", n, err)
		}
		d.Close()
	}
}
