package auth

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// plan is the detail lines of SQLite's plan for query.
func plan(t *testing.T, d *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := d.Query(`EXPLAIN QUERY PLAN `+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	return out
}

// What every authenticated request runs is point reads only: no table is
// scanned, whatever the number of sessions, tokens and profiles.
func TestLookupStatementsReadPointsOnly(t *testing.T) {
	_, _, d := newStoresDB(t)
	for name, q := range map[string]string{"session": sessionLookupSQL, "app token": appTokenLookupSQL} {
		args := []any{"x"}
		if name == "app token" {
			args = []any{"x", "", ""}
		}
		p := plan(t, d, q, args...)
		if len(p) == 0 {
			t.Fatalf("%s: no plan", name)
		}
		t.Logf("%s plan:\n%s", name, strings.Join(p, "\n"))
		for _, line := range p {
			if strings.HasPrefix(line, "SCAN") {
				t.Errorf("%s: %q scans a table; plan:\n%s", name, line, strings.Join(p, "\n"))
			}
		}
	}
}

// The key the lookup joins device_history on is the one SetHistory wrote.
func TestDeviceKeySQLIsDeviceKey(t *testing.T) {
	ctx := context.Background()
	s, ss, d := newStoresDB(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret-pass")
	if _, err := ss.Create(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.CreateDevice(ctx, a.ID, DeviceInfo{ID: "tv-000001", Name: "TV"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ss.CreateDevice(ctx, a.ID, DeviceInfo{Name: "an app that sent no id"}); err != nil {
		t.Fatal(err)
	}
	// a web session that carries a device id is still a session, not an install
	if _, err := d.Exec(`INSERT INTO sessions(token, user_id, device_id) VALUES('w', ?, 'ignored')`, a.ID); err != nil {
		t.Fatal(err)
	}
	devs, err := ss.Devices(ctx, a.ID)
	if err != nil || len(devs) != 4 {
		t.Fatalf("%d devices, %v", len(devs), err)
	}
	for _, dv := range devs {
		var got string
		if err := d.QueryRow(`SELECT `+deviceKeySQL+` FROM sessions s WHERE s.rowid=?`, dv.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if want := deviceKey(dv); got != want {
			t.Errorf("%s session %d: SQL says %q, Go says %q", dv.Kind, dv.ID, got, want)
		}
	}
}

// The device switch is seen by a lookup of a browser's session and of an
// app's, and a managed account is seen by its name and uid.
func TestLookupSeesTheDeviceSwitchOfABrowserAndAnApp(t *testing.T) {
	ctx := context.Background()
	s, ss := newStores(t)
	a, _ := s.CreateFirstAdmin(ctx, "Papa", "secret-pass")
	m, err := s.CreateManaged(ctx, a.ID, "Гостиная")
	if err != nil {
		t.Fatal(err)
	}
	web, _ := ss.Create(ctx, a.ID)
	app, _ := ss.CreateDevice(ctx, a.ID, DeviceInfo{ID: "tv-000001", Name: "Apple TV"})
	other, _ := ss.Create(ctx, a.ID)
	for _, tok := range []string{web, app} {
		_, sess, err := ss.LookupSession(ctx, tok)
		if err != nil || sess.HistoryUserID != 0 || sess.HistoryName != "" || sess.HistoryUID != "" {
			t.Fatalf("before the switch: %+v %v", sess, err)
		}
		if err := ss.SetHistory(ctx, sess, m.ID, true); err != nil {
			t.Fatal(err)
		}
		u, sess, err := ss.LookupSession(ctx, tok)
		if err != nil || u.ID != a.ID || sess.HistoryUserID != m.ID || sess.HistoryName != "Гостиная" || sess.HistoryUID != m.UID || m.UID == "" {
			t.Fatalf("after the switch: %+v %v", sess, err)
		}
	}
	// another browser of the same person is not switched
	if _, sess, err := ss.LookupSession(ctx, other); err != nil || sess.HistoryUserID != 0 {
		t.Fatalf("another browser: %+v %v", sess, err)
	}
	// a token that is not a session, and an empty one
	for _, tok := range []string{"", "nope"} {
		if _, _, err := ss.LookupSession(ctx, tok); err != ErrNotFound {
			t.Errorf("%q: %v", tok, err)
		}
	}
}
