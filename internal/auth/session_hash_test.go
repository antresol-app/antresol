package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// The sessions table holds sha256(token), never the token: a copy of the
// database does not sign anyone in.
func TestSessionTokensStoredHashed(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	web, err := ss.Create(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := ss.CreateDevice(ctx, a.ID, DeviceInfo{ID: "phone-0001", Name: "iPhone"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tok := range []string{web, dev} {
		var n int
		if err := d.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token=?`, tok).Scan(&n); err != nil || n != 0 {
			t.Fatalf("plaintext token stored: n=%d err=%v", n, err)
		}
		if err := d.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token=?`, sha256Hex(tok)).Scan(&n); err != nil || n != 1 {
			t.Fatalf("hash row: n=%d err=%v", n, err)
		}
		if TokenHash(tok) != sha256Hex(tok) {
			t.Fatalf("TokenHash %q", TokenHash(tok))
		}
		if _, _, err := ss.LookupSession(ctx, tok); err != nil {
			t.Fatalf("lookup: %v", err)
		}
		// the stored value itself is not a token
		if _, _, err := ss.LookupSession(ctx, sha256Hex(tok)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("hash accepted as a token: %v", err)
		}
	}
	// RevokeUser keeps the caller's own session (given as the token)
	other, _ := ss.Create(ctx, a.ID)
	if err := ss.RevokeUser(ctx, a.ID, web); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Lookup(ctx, web); err != nil {
		t.Fatalf("kept session: %v", err)
	}
	for _, tok := range []string{dev, other} {
		if _, err := ss.Lookup(ctx, tok); !errors.Is(err, ErrNotFound) {
			t.Fatalf("not revoked: %v", err)
		}
	}
	if err := ss.Revoke(ctx, web); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.Lookup(ctx, web); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked: %v", err)
	}
}

// The migration hashes rows written before it in place: a signed-in
// browser, a signed-in device and a media key minted before the upgrade
// keep working, and the device keeps its id.
func TestMigrationHashesExistingSessions(t *testing.T) {
	ctx := context.Background()
	p := filepath.Join(t.TempDir(), "v.db")
	d, err := db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(d)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret")
	const web = "webtokenwebtokenwebtokenwebtokenwebtokenweb"
	const dev = "devtokendevtokendevtokendevtokendevtokendev"
	if _, err := d.Exec(`INSERT INTO sessions(token, user_id) VALUES(?,?)`, web, a.ID); err != nil {
		t.Fatal(err)
	}
	res, err := d.Exec(`INSERT INTO sessions(token, user_id, kind, device_id, device_name) VALUES(?,?,?,?,?)`, dev, a.ID, KindNative, "phone-0001", "iPhone")
	if err != nil {
		t.Fatal(err)
	}
	devID, _ := res.LastInsertId()
	// pretend the rows predate the hashing migration
	if _, err := d.Exec(`DELETE FROM schema_migrations WHERE version=20`); err != nil {
		t.Fatal(err)
	}
	keys := NewMediaKeys(make([]byte, 32), NewSessions(d))
	key, _ := keys.Issue(Session{ID: devID, UserID: a.ID, Kind: KindNative}, dev, 12*time.Hour)
	d.Close()

	d, err = db.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	var n int
	if err := d.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token IN (?,?)`, web, dev).Scan(&n); err != nil || n != 0 {
		t.Fatalf("plaintext left: n=%d err=%v", n, err)
	}
	ss := NewSessions(d)
	if _, ws, err := ss.LookupSession(ctx, web); err != nil || ws.Kind != KindWeb {
		t.Fatalf("web after migration: %+v %v", ws, err)
	}
	_, ds, err := ss.LookupSession(ctx, dev)
	if err != nil || ds.ID != devID || ds.Device.ID != "phone-0001" {
		t.Fatalf("device after migration: %+v %v", ds, err)
	}
	keys = NewMediaKeys(make([]byte, 32), ss)
	if u, ks, err := keys.Verify(ctx, key); err != nil || u.ID != a.ID || ks.ID != devID {
		t.Fatalf("pre-migration key: %+v %v", ks, err)
	}
	if err := ss.RevokeDevice(ctx, a.ID, devID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := keys.Verify(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("key after revoke: %v", err)
	}
}
