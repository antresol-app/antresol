package iptv

import (
	"context"
	"database/sql"
	"path/filepath"
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

func newTestStore(t *testing.T) *Store {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	return &Store{DB: openTestDB(t), Now: func() time.Time { return now }}
}

func TestMigration0080Applies(t *testing.T) {
	d := openTestDB(t)
	for _, tbl := range []string{"iptv_sources", "iptv_epg_urls", "iptv_members", "iptv_programmes", "iptv_xml_channels", "iptv_sets", "iptv_set_assign"} {
		var n int
		if err := d.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s: n=%d err=%v", tbl, n, err)
		}
	}
	if _, err := d.Exec(`INSERT INTO channels (number, name) VALUES (1, 'Own')`); err != nil {
		t.Fatal(err)
	}
	var kind, why string
	var manual int
	if err := d.QueryRow(`SELECT kind, number_manual, why FROM channels WHERE number=1`).Scan(&kind, &manual, &why); err != nil {
		t.Fatal(err)
	}
	if kind != "antresol" || manual != 0 || why != "" {
		t.Fatalf("defaults: kind=%q manual=%d why=%q", kind, manual, why)
	}
	if _, err := d.Exec(`SELECT set_id FROM app_tokens LIMIT 1`); err != nil {
		t.Fatalf("app_tokens.set_id: %v", err)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	shown := true
	src, err := s.SaveSource(ctx, Source{
		Name: "Synthetic", Kind: KindM3U, URLEnc: []byte("sealed-url"),
		DefaultShown: &shown, PresetTimeshift: true, PresetAdult: true,
		RefreshHours: 24, RefreshOnOpen: true, Profiles: []int64{2, 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if src.ID == 0 {
		t.Fatal("no id")
	}
	e, err := s.SaveEPGURL(ctx, EPGURL{SourceID: src.ID, URLEnc: []byte("sealed-epg"), RefreshHours: 12})
	if err != nil {
		t.Fatal(err)
	}
	srcs, err := s.Sources(ctx)
	if err != nil || len(srcs) != 1 {
		t.Fatalf("sources: %v %v", srcs, err)
	}
	got := srcs[0]
	if got.Name != "Synthetic" || string(got.URLEnc) != "sealed-url" || got.DefaultShown == nil || !*got.DefaultShown ||
		!got.PresetAdult || !got.PresetTimeshift || got.RefreshHours != 24 || !got.RefreshOnOpen || len(got.Profiles) != 2 {
		t.Fatalf("source round trip: %+v", got)
	}
	eps, err := s.EPGURLs(ctx, src.ID)
	if err != nil || len(eps) != 1 || eps[0].ID != e.ID || string(eps[0].URLEnc) != "sealed-epg" || eps[0].RefreshHours != 12 {
		t.Fatalf("epg urls: %+v %v", eps, err)
	}

	// Two own channel rows the members point at.
	var ch1, ch2 int64
	for i, p := range []*int64{&ch1, &ch2} {
		res, err := s.DB.Exec(`INSERT INTO channels (number, name, kind) VALUES (?, ?, 'external')`, 100+i, "Ext")
		if err != nil {
			t.Fatal(err)
		}
		*p, _ = res.LastInsertId()
	}
	first := []Member{
		{Ident: "tvg:one", TvgID: "one", Name: "Channel One", Group: "News", URLEnc: []byte("u1"), ChannelID: ch1, Present: true},
		{Ident: "tvg:two", TvgID: "two", Name: "Channel Two", Group: "Kids", URLEnc: []byte("u2"), ChannelID: ch2, Present: true, UserAgent: "UA/1"},
	}
	if err := s.ReplaceMembers(ctx, src.ID, first); err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceMembers(ctx, src.ID, []Member{
		{Ident: "tvg:one", TvgID: "one", TvgName: "One", Name: "Channel One HD", Group: "News", URLEnc: []byte("u1b"), Present: true},
	}); err != nil {
		t.Fatal(err)
	}
	ms, err := s.Members(ctx, src.ID)
	if err != nil || len(ms) != 2 {
		t.Fatalf("members: %+v %v", ms, err)
	}
	by := map[string]Member{}
	for _, m := range ms {
		by[m.Ident] = m
	}
	one, two := by["tvg:one"], by["tvg:two"]
	if !one.Present || one.TvgName != "One" || one.Name != "Channel One HD" || string(one.URLEnc) != "u1b" || one.ChannelID != ch1 {
		t.Fatalf("kept member: %+v", one)
	}
	if two.Present || two.ChannelID != ch2 || two.UserAgent != "UA/1" {
		t.Fatalf("dropped member: %+v", two)
	}

	if err := s.DeleteEPGURL(ctx, e.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	if ms, _ := s.Members(ctx, src.ID); len(ms) != 0 {
		t.Fatalf("members after delete: %d", len(ms))
	}
}
