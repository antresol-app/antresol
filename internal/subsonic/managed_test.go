package subsonic

import (
	"context"
	"testing"
)

// A music app signed in with a managed account's app password (the
// person's name, the TV's password) is the person: its stars stay the
// person's, its plays are the account's history, and «recent» lists the
// account's plays. The person's own password keeps the person's plays.
func TestManagedAppPasswordKeepsTheTVsPlays(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	al, tr := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Группа крови"})
	tv, err := e.users.CreateManaged(ctx, e.admin.ID, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	pw, err := e.users.SetManagedAppPassword(ctx, e.admin.ID, tv.ID)
	if err != nil {
		t.Fatal(err)
	}
	own := e.app
	e.app = pw // e.call signs in with e.app
	e.mustOK(e.call("scrobble", "id", itoa(tr[0])))
	e.mustOK(e.call("star", "id", itoa(tr[0])))
	plays := func(uid int64) int {
		var n int
		if err := e.db.QueryRow(`SELECT COUNT(*) FROM plays WHERE user_id=?`, uid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if plays(tv.ID) != 1 || plays(e.admin.ID) != 0 {
		t.Fatalf("plays: tv %d person %d", plays(tv.ID), plays(e.admin.ID))
	}
	if ids, _ := e.music.Starred(ctx, e.admin.ID); len(ids) != 1 {
		t.Fatalf("star not the person's: %v", ids)
	}
	recent := func() int {
		l, _ := e.mustOK(e.call("getAlbumList2", "type", "recent"))["albumList2"].(map[string]any)["album"].([]any)
		return len(l)
	}
	if n := recent(); n != 1 {
		t.Fatalf("tv recent %d", n)
	}
	e.app = own
	if n := recent(); n != 0 {
		t.Fatalf("person's recent %d (album %d)", n, al)
	}
	e.mustOK(e.call("scrobble", "id", itoa(tr[0])))
	if plays(e.admin.ID) != 1 || plays(tv.ID) != 1 {
		t.Fatalf("own password: tv %d person %d", plays(tv.ID), plays(e.admin.ID))
	}
}
