package notify

import (
	"testing"
	"time"
)

// The minute flush does not touch the old rows (its deletes take the write
// lock even when nothing matches); prune, run hourly, deletes them.
func TestPruneIsOutOfTheFlush(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "anya", false, false)
	day := 24 * time.Hour
	at := func(ago time.Duration) int64 { return f.clock.Add(-ago).Unix() }
	ev := func(key string, created, sent, tg any) {
		t.Helper()
		if _, err := f.db.Exec(`INSERT INTO notify_events (user_id, topic, key, created_at, sent_at, tg_sent_at) VALUES (?,?,?,?,?,?)`,
			u.ID, "available", key, created, sent, tg); err != nil {
			t.Fatal(err)
		}
	}
	ev("old-sent", at(100*day), at(91*day), at(91*day))
	ev("recent-sent", at(10*day), at(day), at(day))
	ev("old-pending", at(31*day), nil, nil)
	ev("fresh-pending", at(day), nil, nil)
	for _, seen := range []struct {
		key string
		ago time.Duration
	}{{"old", 366 * day}, {"fresh", day}} {
		if _, err := f.db.Exec(`INSERT INTO notify_seen (user_id, key, at) VALUES (?,?,?)`, u.ID, seen.key, at(seen.ago)); err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string) (n int) {
		t.Helper()
		if err := f.db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	f.flush(t)
	if n, s := count(`SELECT COUNT(*) FROM notify_events`), count(`SELECT COUNT(*) FROM notify_seen`); n != 4 || s != 2 {
		t.Fatalf("flush pruned: %d events, %d seen", n, s)
	}
	if err := f.svc.prune(f.ctx, f.clock); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT COUNT(*) FROM notify_events WHERE key IN ('recent-sent','fresh-pending')`); n != 2 {
		t.Fatalf("kept %d of the 2 recent events", n)
	}
	if n, s := count(`SELECT COUNT(*) FROM notify_events`), count(`SELECT COUNT(*) FROM notify_seen WHERE key='fresh'`); n != 2 || s != 1 {
		t.Fatalf("after prune: %d events, fresh seen %d", n, s)
	}
	if n := count(`SELECT COUNT(*) FROM notify_seen`); n != 1 {
		t.Fatalf("%d seen left", n)
	}
}
