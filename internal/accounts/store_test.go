package accounts

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

func newStore(t *testing.T) (*Store, int64) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	r, err := d.Exec(`INSERT INTO users(name, avatar) VALUES('u', 'a')`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := r.LastInsertId()
	key := bytes.Repeat([]byte{7}, 32)
	s := &Store{DB: d, Key: key}
	if err := s.SetNickname(context.Background(), uid, "shikimori", "nick", 42); err != nil {
		t.Fatal(err)
	}
	return s, uid
}

func addTitle(t *testing.T, s *Store, tmdb int64) int64 {
	t.Helper()
	r, err := s.DB.Exec(`INSERT INTO tracked_titles(kind, tmdb_id, name) VALUES('series', ?, 'x')`, tmdb)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	return id
}

func TestTokensEncryptedAtRest(t *testing.T) {
	s, uid := newStore(t)
	ctx := context.Background()
	exp := time.Unix(2000000000, 0)
	if err := s.SetTokens(ctx, uid, "shikimori", Tokens{Access: "access-secret-123", Refresh: "refresh-secret-456", Expires: exp}); err != nil {
		t.Fatal(err)
	}
	var a, r []byte
	if err := s.DB.QueryRow(`SELECT access_enc, refresh_enc FROM account_links WHERE user_id=?`, uid).Scan(&a, &r); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(a, []byte("access-secret-123")) || bytes.Contains(r, []byte("refresh-secret-456")) {
		t.Fatal("token stored in plaintext")
	}
	got, ok, err := s.Tokens(ctx, uid, "shikimori")
	if err != nil || !ok || got.Access != "access-secret-123" || got.Refresh != "refresh-secret-456" || !got.Expires.Equal(exp) {
		t.Fatalf("got=%+v ok=%v err=%v", got, ok, err)
	}
	bad := &Store{DB: s.DB, Key: bytes.Repeat([]byte{9}, 32)}
	if got, _, err := bad.Tokens(ctx, uid, "shikimori"); err == nil {
		t.Fatalf("wrong key returned %+v without error", got)
	}
}

func TestOutboxCoalesces(t *testing.T) {
	s, uid := newStore(t)
	ctx := context.Background()
	tid := addTitle(t, s, 1)
	now := time.Now()
	for _, op := range []string{"upsert", "upsert", "delete"} {
		if err := s.Enqueue(ctx, uid, "shikimori", tid, op, now); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.Outbox(ctx, uid, "shikimori")
	if err != nil || len(items) != 1 || items[0].Op != "delete" || items[0].TitleID != tid {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if err := s.Failed(ctx, items[0].ID, "boom", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	items, _ = s.Outbox(ctx, uid, "shikimori")
	if items[0].Attempts != 1 || items[0].LastError != "boom" || items[0].NextAt.Unix() != now.Add(time.Minute).Unix() {
		t.Fatalf("items=%+v", items)
	}
	if err := s.Done(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	if items, _ = s.Outbox(ctx, uid, "shikimori"); len(items) != 0 {
		t.Fatalf("items=%+v", items)
	}
}

func TestUnlinkRemovesRatesAndOutbox(t *testing.T) {
	s, uid := newStore(t)
	ctx := context.Background()
	tid := addTitle(t, s, 1)
	if err := s.SetRate(ctx, Rate{UserID: uid, Provider: "shikimori", TitleID: tid, TargetID: 5, RateID: 6}); err != nil {
		t.Fatal(err)
	}
	if err := s.Enqueue(ctx, uid, "shikimori", tid, "upsert", time.Now()); err != nil {
		t.Fatal(err)
	}
	if rs, _ := s.Rates(ctx, uid, "shikimori"); len(rs) != 1 || rs[0].RateID != 6 {
		t.Fatalf("rates=%+v", rs)
	}
	if err := s.Unlink(ctx, uid, "shikimori"); err != nil {
		t.Fatal(err)
	}
	for _, tb := range []string{"account_links", "account_rates", "account_outbox"} {
		var n int
		s.DB.QueryRow(`SELECT count(*) FROM ` + tb).Scan(&n)
		if n != 0 {
			t.Fatalf("%s has %d rows", tb, n)
		}
	}
}

func TestLinkCascadeOnUserDelete(t *testing.T) {
	s, uid := newStore(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(`DELETE FROM users WHERE id=?`, uid); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.Link(ctx, uid, "shikimori"); err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestLinkStateRoundTrip(t *testing.T) {
	s, uid := newStore(t)
	ctx := context.Background()
	l, ok, err := s.Link(ctx, uid, "shikimori")
	if err != nil || !ok || l.Nickname != "nick" || l.ExternalID != 42 || l.WriteState != "unasked" {
		t.Fatalf("l=%+v ok=%v err=%v", l, ok, err)
	}
	if err := s.SetWriteState(ctx, uid, "shikimori", "enabled"); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1900000000, 0)
	if err := s.SetStatus(ctx, uid, "shikimori", "error", "bad", at); err != nil {
		t.Fatal(err)
	}
	ls, err := s.Links(ctx, "shikimori")
	if err != nil || len(ls) != 1 || ls[0].WriteState != "enabled" || ls[0].Status != "error" || ls[0].LastError != "bad" || !ls[0].LastSync.Equal(at) {
		t.Fatalf("ls=%+v err=%v", ls, err)
	}
}

func TestTokensBoundToRow(t *testing.T) {
	s, uid := newStore(t)
	ctx := context.Background()
	r, _ := s.DB.Exec(`INSERT INTO users(name, avatar) VALUES('v', 'a')`)
	uid2, _ := r.LastInsertId()
	if err := s.SetNickname(ctx, uid2, "shikimori", "n2", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTokens(ctx, uid, "shikimori", Tokens{Access: "a1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`UPDATE account_links SET access_enc=(SELECT access_enc FROM account_links WHERE user_id=?) WHERE user_id=?`, uid, uid2); err != nil {
		t.Fatal(err)
	}
	if got, _, err := s.Tokens(ctx, uid2, "shikimori"); err == nil {
		t.Fatalf("copied ciphertext decrypted: %+v", got)
	}
}

func TestSetTokensRejectsEmptyAccess(t *testing.T) {
	s, uid := newStore(t)
	ctx := context.Background()
	if err := s.SetTokens(ctx, uid, "shikimori", Tokens{Access: "keep"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTokens(ctx, uid, "shikimori", Tokens{Refresh: "r"}); err == nil {
		t.Fatal("empty access accepted")
	}
	if got, _, _ := s.Tokens(ctx, uid, "shikimori"); got.Access != "keep" {
		t.Fatalf("got=%+v", got)
	}
}

func TestClientSettings(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if _, _, ok, err := s.ClientSettings(ctx, "shikimori"); ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if err := s.SetClientSettings(ctx, "shikimori", "cid", "sec-value"); err != nil {
		t.Fatal(err)
	}
	var enc []byte
	s.DB.QueryRow(`SELECT secret_enc FROM account_settings`).Scan(&enc)
	if bytes.Contains(enc, []byte("sec-value")) {
		t.Fatal("secret in plaintext")
	}
	id, sec, ok, err := s.ClientSettings(ctx, "shikimori")
	if err != nil || !ok || id != "cid" || sec != "sec-value" {
		t.Fatalf("%q %q %v %v", id, sec, ok, err)
	}
}

// TestDoneAtKeepsNewerIntent: a title queued again while its push ran (even
// within the same second) survives the push's DoneAt.
func TestDoneAtKeepsNewerIntent(t *testing.T) {
	s, uid := newStore(t)
	ctx := context.Background()
	tid := addTitle(t, s, 1)
	now := time.Now()
	if err := s.Enqueue(ctx, uid, "shikimori", tid, "upsert", now); err != nil {
		t.Fatal(err)
	}
	read, _ := s.Outbox(ctx, uid, "shikimori")
	if err := s.Enqueue(ctx, uid, "shikimori", tid, "upsert", now); err != nil {
		t.Fatal(err)
	}
	if err := s.DoneAt(ctx, read[0].ID, read[0].Seq); err != nil {
		t.Fatal(err)
	}
	items, _ := s.Outbox(ctx, uid, "shikimori")
	if len(items) != 1 || items[0].Seq == read[0].Seq {
		t.Fatalf("newer intent lost: %+v (read %+v)", items, read)
	}
	if err := s.DoneAt(ctx, items[0].ID, items[0].Seq); err != nil {
		t.Fatal(err)
	}
	if items, _ := s.Outbox(ctx, uid, "shikimori"); len(items) != 0 {
		t.Fatalf("items %+v", items)
	}
}

func TestClearTokens(t *testing.T) {
	s, uid := newStore(t)
	ctx := context.Background()
	if err := s.SetTokens(ctx, uid, "shikimori", Tokens{Access: "a", Refresh: "r", Expires: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearTokens(ctx, uid, "shikimori"); err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.Tokens(ctx, uid, "shikimori")
	if err != nil || tok.Access != "" || tok.Refresh != "" || !tok.Expires.IsZero() {
		t.Fatalf("tokens %+v %v", tok, err)
	}
	if _, ok, _ := s.Link(ctx, uid, "shikimori"); !ok {
		t.Fatal("link removed")
	}
}
