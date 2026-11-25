package db

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
)

// serviceSilent: the tables a service profile (users.service = 1) writes
// nothing to: what a profile watched, listened to or tuned in last. Each
// has a BEFORE INSERT trigger (migration 0119) that drops the row without
// an error, and a statement here that inserts one row for the user ?.
var serviceSilent = map[string]string{
	"progress":          `INSERT INTO progress(user_id, item_id, position_ms, duration_ms, finished, updated_at) VALUES(?, 1, 5, 10, 0, 't')`,
	"progress_cleared":  `INSERT INTO progress_cleared(user_id, item_id, cleared_at) VALUES(?, 1, 't')`,
	"watchlist":         `INSERT INTO watchlist(user_id, item_id, added_at) VALUES(?, 1, 't')`,
	"annotations":       `INSERT INTO annotations(user_id, item_id, id, kind, locator, created_at, updated_at, rev) VALUES(?, 1, 'a', 'note', 'l', 't', 't', 1)`,
	"watch_time":        `INSERT INTO watch_time(user_id, item_id, day, ms) VALUES(?, 1, '2026-01-01', 5)`,
	"plays":             `INSERT INTO plays(user_id, item_id, played_at) VALUES(?, 1, 't')`,
	"screen_time_usage": `INSERT INTO screen_time_usage(user_id, day, category, item_id, seconds) VALUES(?, '2026-01-01', 'video', 1, 5)`,
	"channel_last":      `INSERT INTO channel_last(user_id, channel_id) VALUES(?, 1)`,
	"title_marks":       `INSERT INTO title_marks(user_id, title_id, state, updated_at) VALUES(?, 1, 'planned', 't')`,
}

// serviceKept: the other tables with a user_id, decided on: a profile's
// credentials, preferences, lists and settings, not what it watched. A
// service profile may hold these (it signs in, so it has sessions).
var serviceKept = []string{
	"account_links", "account_outbox", "account_rates", "api_key_log", "api_keys", "app_tokens", "channel_favorites",
	"channel_prefs", "channel_removed", "channels", "device_history", "device_pairings", "goodreads_entries", "import_jobs",
	"import_playlists", "iptv_set_assign", "item_owners", "kid_requests", "kindle_sends", "myshows_entries", "notify_addon_prefs",
	"notify_events", "notify_prefs", "notify_seen", "notify_telegram_codes", "notify_verify_log", "playback_flags",
	"playback_prefs", "playlists", "screen_time_bonus", "screen_time_limits", "series_tracks", "sessions", "stars",
	"subtitle_offsets", "title_choices", "ui_prefs", "user_photos",
}

// TestServiceProfileIsSilent: a row a person's profile writes lands, the
// same row of a service profile is dropped, and no statement fails.
func TestServiceProfileIsSilent(t *testing.T) {
	ctx := context.Background()
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	// the statements name items, channels and titles that do not exist: one
	// connection without the foreign keys (a dropped row needs none)
	c, err := d.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	var person, service int64
	for _, u := range []struct {
		name string
		svc  int
		id   *int64
	}{{"person", 0, &person}, {"machine", 1, &service}} {
		res, err := c.ExecContext(ctx, `INSERT INTO users(name, avatar, service) VALUES(?, 'grad-1', ?)`, u.name, u.svc)
		if err != nil {
			t.Fatal(err)
		}
		if *u.id, err = res.LastInsertId(); err != nil {
			t.Fatal(err)
		}
	}
	for table, stmt := range serviceSilent {
		for _, uid := range []int64{person, service} {
			if _, err := c.ExecContext(ctx, stmt, uid); err != nil {
				t.Fatalf("%s: insert for user %d failed: %v", table, uid, err)
			}
		}
		for uid, want := range map[int64]int{person: 1, service: 0} {
			var n int
			if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE user_id=?`, uid).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != want {
				t.Errorf("%s: user %d has %d rows, want %d", table, uid, n, want)
			}
		}
	}
	// an upsert of a row that cannot exist yet is dropped as well, not failed
	if _, err := c.ExecContext(ctx, `INSERT INTO watch_time(user_id,item_id,day,ms) VALUES(?,1,'2026-01-01',9)
		ON CONFLICT(user_id,item_id,day) DO UPDATE SET ms=watch_time.ms+excluded.ms`, service); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := c.QueryRowContext(ctx, `SELECT COUNT(*) FROM watch_time WHERE user_id=?`, service).Scan(&n); err != nil || n != 0 {
		t.Fatalf("upsert: %d rows, %v", n, err)
	}
}

// TestServiceProfileHistoryTables: every table with a user_id column is
// either silent for a service profile or listed as kept. A new table that
// holds what a profile watched or listened to must be added to
// serviceSilent (with a trigger in a new migration), any other to
// serviceKept.
func TestServiceProfileHistoryTables(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, err := d.Query(`SELECT m.name FROM sqlite_master m JOIN pragma_table_info(m.name) c ON c.name = 'user_id'
		WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' ORDER BY m.name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		seen++
		if _, ok := serviceSilent[name]; ok == slices.Contains(serviceKept, name) {
			t.Errorf("table %s has a user_id: it must be in exactly one of serviceSilent and serviceKept (silent for a service profile, or decided to be kept)", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := len(serviceSilent) + len(serviceKept); seen != want {
		t.Errorf("%d tables have a user_id, the two lists name %d: one is gone or misspelt", seen, want)
	}
}
