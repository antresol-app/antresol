package db

import (
	"database/sql"
	"path/filepath"
	"regexp"
	"testing"
)

// TestMigration0093_UsersUID: the profiles a database already has get
// distinct uids, and a profile inserted later without one gets its own.
func TestMigration0093_UsersUID(t *testing.T) {
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);
		INSERT INTO users(name) VALUES ('a'), ('b'), ('c')`); err != nil {
		t.Fatal(err)
	}
	body, err := migrationsFS.ReadFile("migrations/0093_users_uid.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(string(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO users(name) VALUES ('d')`); err != nil {
		t.Fatal(err)
	}
	shape := regexp.MustCompile(`^[0-9a-f]{32}$`)
	rows, err := raw.Query(`SELECT name, COALESCE(uid, '') FROM users`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	n := 0
	for rows.Next() {
		var name, uid string
		if err := rows.Scan(&name, &uid); err != nil {
			t.Fatal(err)
		}
		if !shape.MatchString(uid) || seen[uid] {
			t.Fatalf("%s: uid %q", name, uid)
		}
		seen[uid] = true
		n++
	}
	if n != 4 {
		t.Fatalf("%d profiles", n)
	}
}
