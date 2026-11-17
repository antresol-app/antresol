package auth

import (
	"context"
	"regexp"
	"testing"
)

var uidShape = regexp.MustCompile(`^[0-9a-f]{32}$`)

// TestUserUID: every profile gets a uid of its own; a profile created in
// place of a deleted one gets the deleted one's id back (users.id has no
// AUTOINCREMENT) but never its uid. Profiles inserted without one (a
// raw INSERT) get one from the database.
func TestUserUID(t *testing.T) {
	ctx := context.Background()
	s, _, d := newStoresDB(t)
	a, err := s.CreateFirstAdmin(ctx, "Papa", "secret")
	if err != nil {
		t.Fatal(err)
	}
	k, err := s.CreateUser(ctx, NewUser{Name: "Kid", IsKid: true})
	if err != nil {
		t.Fatal(err)
	}
	if !uidShape.MatchString(a.UID) || !uidShape.MatchString(k.UID) || a.UID == k.UID {
		t.Fatalf("uids %q %q", a.UID, k.UID)
	}
	if got, err := s.Get(ctx, k.ID); err != nil || got.UID != k.UID {
		t.Fatalf("get: %q %v, want %q", got.UID, err, k.UID)
	}
	if err := s.Delete(ctx, k.ID); err != nil {
		t.Fatal(err)
	}
	n, err := s.CreateUser(ctx, NewUser{Name: "Kid2"})
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != k.ID {
		t.Fatalf("id %d not reused (was %d): the test proves nothing", n.ID, k.ID)
	}
	if !uidShape.MatchString(n.UID) || n.UID == k.UID {
		t.Fatalf("reused id %d: uid %q, the deleted profile's was %q", n.ID, n.UID, k.UID)
	}
	if _, err := d.Exec(`INSERT INTO users(name, avatar) VALUES('Raw', 'grad-1')`); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, u := range list {
		if !uidShape.MatchString(u.UID) || seen[u.UID] {
			t.Fatalf("list: %q (%s)", u.UID, u.Name)
		}
		seen[u.UID] = true
	}
	// a uid is never changed
	if _, err := d.Exec(`UPDATE users SET uid = 'x' WHERE id = ?`, a.ID); err == nil {
		t.Fatal("uid changed")
	}
}
