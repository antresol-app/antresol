package progress

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestMergeIntoNewerWins(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	to, from := e.user("Papa"), e.user("TV")
	a, b, c, d := e.movie("A"), e.movie("B"), e.movie("C"), e.movie("D")

	// a: only the device watched it
	e.must(e.s.PlayedAt(ctx, from, a, 60_000, 600_000, time.Time{}))
	// b: the person later than the device: the person's stays
	e.must(e.s.PlayedAt(ctx, from, b, 10_000, 600_000, time.Time{}))
	e.must(e.s.PlayedAt(ctx, to, b, 20_000, 600_000, time.Time{}))
	// c: the device later: the device's wins
	e.must(e.s.PlayedAt(ctx, to, c, 10_000, 600_000, time.Time{}))
	e.must(e.s.PlayedAt(ctx, from, c, 30_000, 600_000, time.Time{}))
	// d: the person watched it, the device later marked it unwatched
	e.must(e.s.MarkWatched(ctx, to, d, true))
	e.must(e.s.MarkWatched(ctx, from, d, false))

	called := 0
	e.s.OnWatchedChange = func(context.Context, int64, int64) { called++ }
	e.must(e.s.MergeInto(ctx, from, to))
	if called != 0 {
		t.Fatalf("a merge announced %d watched changes", called)
	}

	pos := func(item int64) int64 {
		t.Helper()
		got, err := e.s.Get(ctx, to, item)
		if err != nil {
			t.Fatalf("item %d: %v", item, err)
		}
		return got.PositionMs
	}
	if pos(a) != 60_000 || pos(b) != 20_000 || pos(c) != 30_000 {
		t.Fatalf("a %d b %d c %d", pos(a), pos(b), pos(c))
	}
	if _, err := e.s.Get(ctx, to, d); err != ErrNotFound {
		t.Fatalf("d kept: %v", err)
	}
	if _, ok, _ := e.s.ClearedAt(ctx, to, d); !ok {
		t.Fatal("d tombstone not moved")
	}
	// played time adds up into the person's
	var ms sql.NullInt64
	e.s.db.QueryRow(`SELECT SUM(ms) FROM watch_time WHERE user_id=?`, to).Scan(&ms)
	var fromMs sql.NullInt64
	e.s.db.QueryRow(`SELECT SUM(ms) FROM watch_time WHERE user_id=?`, from).Scan(&fromMs)
	if fromMs.Valid && fromMs.Int64 != 0 {
		t.Fatalf("watch_time left on the device: %d", fromMs.Int64)
	}
	_ = ms
}

func TestMergeIntoMovesPlays(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	to, from := e.user("Papa"), e.user("TV")
	m := e.movie("Track-ish")
	if _, err := e.s.db.Exec(`INSERT INTO plays(user_id,item_id,played_at) VALUES(?,?,?)`, from, m, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.db.Exec(`INSERT INTO watch_time(user_id,item_id,day,ms) VALUES(?,?,?,?),(?,?,?,?)`,
		from, m, "2026-01-01", 1000, to, m, "2026-01-01", 500); err != nil {
		t.Fatal(err)
	}
	e.must(e.s.MergeInto(ctx, from, to))
	var n int
	e.s.db.QueryRow(`SELECT COUNT(*) FROM plays WHERE user_id=?`, to).Scan(&n)
	if n != 1 {
		t.Fatalf("plays %d", n)
	}
	var ms int64
	e.s.db.QueryRow(`SELECT ms FROM watch_time WHERE user_id=? AND item_id=?`, to, m).Scan(&ms)
	if ms != 1500 {
		t.Fatalf("watch_time %d", ms)
	}
}

// The merge and the deletion of the account are one transaction: what the
// account held is the person's, and the account and its device mapping are
// gone; nothing but the owner's own account can be merged and deleted.
func TestMergeAndDeleteIsOneStep(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	to := e.user("Papa")
	other := e.user("Mama")
	acc, err := e.users.CreateManaged(ctx, to, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	m := e.movie("A")
	e.must(e.s.PlayedAt(ctx, acc.ID, m, 60_000, 600_000, time.Time{}))

	// someone else's account: refused, nothing moves
	if _, err := e.s.MergeAndDelete(ctx, acc.ID, other, false); err != ErrNotFound {
		t.Fatalf("other owner: %v", err)
	}
	if _, err := e.s.Get(ctx, acc.ID, m); err != nil {
		t.Fatalf("history moved for a wrong owner: %v", err)
	}
	// a person is no account
	if _, err := e.s.MergeAndDelete(ctx, other, to, false); err != ErrNotFound {
		t.Fatalf("person as account: %v", err)
	}

	// a device still writes into it: merged, kept
	if _, err := e.s.db.Exec(`INSERT INTO device_history(user_id, device_key, managed_id, enabled) VALUES(?,?,?,1)`, to, "app:tv-000001", acc.ID); err != nil {
		t.Fatal(err)
	}
	deleted, err := e.s.MergeAndDelete(ctx, acc.ID, to, true)
	if err != nil || deleted {
		t.Fatalf("in use: %v %v", deleted, err)
	}
	if got, err := e.s.Get(ctx, to, m); err != nil || got.PositionMs != 60_000 {
		t.Fatalf("merged: %+v %v", got, err)
	}
	if ok, _ := e.users.IsManaged(ctx, acc.ID); !ok {
		t.Fatal("an account in use was deleted")
	}

	// switched off: gone with its mapping
	if _, err := e.s.db.Exec(`UPDATE device_history SET enabled=0 WHERE managed_id=?`, acc.ID); err != nil {
		t.Fatal(err)
	}
	e.must(e.s.PlayedAt(ctx, acc.ID, m, 90_000, 600_000, time.Time{}))
	deleted, err = e.s.MergeAndDelete(ctx, acc.ID, to, true)
	if err != nil || !deleted {
		t.Fatalf("unused: %v %v", deleted, err)
	}
	if got, _ := e.s.Get(ctx, to, m); got.PositionMs != 90_000 {
		t.Fatalf("last write lost: %+v", got)
	}
	if ok, _ := e.users.IsManaged(ctx, acc.ID); ok {
		t.Fatal("the account is still there")
	}
	var n int
	e.s.db.QueryRow(`SELECT COUNT(*) FROM device_history WHERE managed_id=?`, acc.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("mapping left: %d", n)
	}
	// a second call finds nothing
	if _, err := e.s.MergeAndDelete(ctx, acc.ID, to, false); err != ErrNotFound {
		t.Fatalf("twice: %v", err)
	}
}
