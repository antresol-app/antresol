package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
	"github.com/antresol-app/antresol/internal/db"
)

// serviceProfileEnv is a data folder with a database and one person's
// profile, as VAULT_DATA_DIR of the command.
func serviceProfileEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("VAULT_DATA_DIR", dir)
	d, err := db.Open(filepath.Join(dir, "vault.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.NewStore(d).CreateFirstAdmin(context.Background(), "Папа", "secret-pass"); err != nil {
		t.Fatal(err)
	}
	d.Close()
	return dir
}

// runSP runs the command and returns what it printed to stdout.
func runSP(t *testing.T, args ...string) (map[string]string, string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runServiceProfile(args, &out)
	kv := map[string]string{}
	for _, l := range strings.Split(out.String(), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			kv[k] = v
		}
	}
	return kv, out.String(), err
}

func TestServiceProfileCommand(t *testing.T) {
	dir := serviceProfileEnv(t)
	ctx := context.Background()

	kv, _, err := runSP(t, "create", "playback-check")
	if err != nil {
		t.Fatal(err)
	}
	id, err := strconv.ParseInt(kv["id"], 10, 64)
	if err != nil || id == 0 || len(kv["password"]) < 32 {
		t.Fatalf("printed %v", kv)
	}
	open := func() (*auth.Store, func()) {
		d, err := db.Open(filepath.Join(dir, "vault.db"))
		if err != nil {
			t.Fatal(err)
		}
		return auth.NewStore(d), func() { d.Close() }
	}
	store, done := open()
	if u, err := store.Verify(ctx, id, kv["password"]); err != nil || !u.Service || u.IsAdmin {
		t.Fatalf("the printed password does not sign it in: %+v %v", u, err)
	}
	if people, _ := store.List(ctx); len(people) != 1 {
		t.Fatalf("people: %+v", people)
	}
	done()

	// a second one of the name, and a person's name, are refused
	for _, name := range []string{"playback-check", "Папа"} {
		if _, _, err := runSP(t, "create", name); err == nil || !strings.Contains(err.Error(), "taken") {
			t.Errorf("create %q again: %v", name, err)
		}
	}

	// a new password: the id stays, the old password ends
	kv2, _, err := runSP(t, "reset-password", "playback-check")
	if err != nil || kv2["id"] != kv["id"] || kv2["password"] == kv["password"] || kv2["password"] == "" {
		t.Fatalf("reset-password: %v %v", kv2, err)
	}
	store, done = open()
	if _, err := store.Verify(ctx, id, kv["password"]); err == nil {
		t.Error("the old password still signs in")
	}
	if _, err := store.Verify(ctx, id, kv2["password"]); err != nil {
		t.Errorf("the new password: %v", err)
	}
	done()

	// the command never touches a person's profile
	for _, args := range [][]string{{"reset-password", "Папа"}, {"delete", "Папа"}, {"delete", "nobody"}} {
		if _, _, err := runSP(t, args...); err == nil || !strings.Contains(err.Error(), "no service profile") {
			t.Errorf("%v: %v", args, err)
		}
	}
	if _, out, err := runSP(t, "delete", "playback-check"); err != nil || !strings.Contains(out, "removed") {
		t.Fatalf("delete: %q %v", out, err)
	}
	store, done = open()
	defer done()
	if _, err := store.Get(ctx, id); err == nil {
		t.Error("the service profile is still there")
	}
	if people, _ := store.List(ctx); len(people) != 1 {
		t.Errorf("people after: %+v", people)
	}
}

func TestServiceProfileCommandRefusals(t *testing.T) {
	for _, args := range [][]string{nil, {"create"}, {"make", "x"}, {"create", "a", "b"}, {"delete"}} {
		if _, _, err := runSP(t, args...); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Errorf("%v: %v", args, err)
		}
	}
	// no database: it makes none (a wrong folder is not a new server)
	dir := t.TempDir()
	t.Setenv("VAULT_DATA_DIR", dir)
	if _, _, err := runSP(t, "create", "x"); err == nil || !strings.Contains(err.Error(), "no database") {
		t.Errorf("no database: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "vault.db")); err == nil {
		t.Error("the command made a database")
	}
	// a name with nothing in it
	serviceProfileEnv(t)
	if _, _, err := runSP(t, "create", "  "); err == nil || !strings.Contains(err.Error(), "not a name") {
		t.Errorf("an empty name: %v", err)
	}
}
