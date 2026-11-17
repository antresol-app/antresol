package addons

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/db"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func sample(id string) Installed {
	return Installed{
		ID: id, Source: "https://example.com/" + id, Version: "1.0.0", PrevVersion: "0.9.0",
		Manifest: Manifest{
			ID: id, Name: Text{RU: "Имя", EN: "Name"}, Version: "1.0.0", Contract: ContractVersion,
			Platforms:   map[string]Platform{"linux/amd64": {Asset: "a.tar.gz", SHA256: "ab"}},
			Permissions: []string{"events"},
		},
		Enabled:     true,
		InstalledAt: time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC),
	}
}

func TestStorePutGetList(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: openTestDB(t)}
	for _, id := range []string{"zeta", "alpha"} {
		if err := s.Put(ctx, sample(id), "tok-"+id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Get(ctx, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	want := sample("alpha")
	if got.ID != "alpha" || got.Source != want.Source || got.Version != "1.0.0" || got.PrevVersion != "0.9.0" ||
		!got.Enabled || !got.InstalledAt.Equal(want.InstalledAt) || !got.UpdatedAt.Equal(want.UpdatedAt) {
		t.Fatalf("got %+v", got)
	}
	if got.Manifest.Name.RU != "Имя" || got.Manifest.Platforms["linux/amd64"].Asset != "a.tar.gz" || got.Manifest.Permissions[0] != "events" {
		t.Fatalf("manifest lost: %+v", got.Manifest)
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "alpha" || list[1].ID != "zeta" {
		t.Fatalf("list %v %v", list, err)
	}
	if _, err := s.Get(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestStorePutUpsertKeepsInstalledAt(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: openTestDB(t)}
	first := sample("a")
	if err := s.Put(ctx, first, "t1"); err != nil {
		t.Fatal(err)
	}
	second := sample("a")
	second.Version = "2.0.0"
	second.PrevVersion = "1.0.0"
	second.InstalledAt = first.InstalledAt.Add(48 * time.Hour)
	second.UpdatedAt = first.UpdatedAt.Add(48 * time.Hour)
	if err := s.Put(ctx, second, "t2"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, "a")
	if got.Version != "2.0.0" || got.PrevVersion != "1.0.0" {
		t.Fatalf("not updated: %+v", got)
	}
	if !got.InstalledAt.Equal(first.InstalledAt) || !got.UpdatedAt.Equal(second.UpdatedAt) {
		t.Fatalf("times: %+v", got)
	}
	if id, err := s.IDForToken(ctx, "t2"); err != nil || id != "a" {
		t.Fatalf("new token: %q %v", id, err)
	}
	if _, err := s.IDForToken(ctx, "t1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old token still valid: %v", err)
	}
}

func TestStoreSetEnabledDelete(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: openTestDB(t)}
	if err := s.Put(ctx, sample("a"), "t"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnabled(ctx, "a", false); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Get(ctx, "a"); g.Enabled {
		t.Fatal("still enabled")
	}
	if err := s.SetEnabled(ctx, "missing", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := s.Delete(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := s.Delete(ctx, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestStoreTokens(t *testing.T) {
	ctx := context.Background()
	s := &Store{DB: openTestDB(t)}
	if err := s.Put(ctx, sample("a"), "secret-token-xyz"); err != nil {
		t.Fatal(err)
	}
	if id, err := s.IDForToken(ctx, "secret-token-xyz"); err != nil || id != "a" {
		t.Fatalf("hit: %q %v", id, err)
	}
	if _, err := s.IDForToken(ctx, "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("miss: %v", err)
	}
	var hash string
	if err := s.DB.QueryRow(`SELECT token_hash FROM addons WHERE id='a'`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("secret-token-xyz"))
	if hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash %q", hash)
	}
	rows, _ := s.DB.Query(`SELECT * FROM addons`)
	defer rows.Close()
	cols, _ := rows.Columns()
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for _, v := range vals {
			if strings.Contains(toString(v), "secret-token-xyz") {
				t.Fatal("raw token stored")
			}
		}
	}
	if err := s.SetToken(ctx, "a", "new-tok"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IDForToken(ctx, "secret-token-xyz"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old: %v", err)
	}
	if id, err := s.IDForToken(ctx, "new-tok"); err != nil || id != "a" {
		t.Fatalf("new: %q %v", id, err)
	}
	if err := s.SetToken(ctx, "missing", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return ""
}
