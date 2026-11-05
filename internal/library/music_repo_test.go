package library

import (
	"context"
	"testing"
)

// kinoAlbum is «Кино/Группа крови» with tracks 1–3.
func kinoAlbum(t *testing.T, r *Repo) int64 {
	t.Helper()
	var in []TrackInput
	for i, title := range []string{"Группа крови", "Закрой за мной дверь, я ухожу", "Война"} {
		in = append(in, TrackInput{File: FileInput{Path: "/m/Кино/Группа крови/0" + string(rune('1'+i)) + ".flac", Size: 1, Mtime: 1},
			AlbumDir: "/m/Кино/Группа крови", Album: "Группа крови", Title: title, Track: i + 1})
	}
	id, err := r.UpsertAlbumTracks(context.Background(), "Кино", in)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFindAlbumByMBID(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	id := kinoAlbum(t, r)
	const rg = "5e4b8c3e-7c4b-3d07-8d5a-1f2f6a3c0b11"
	if _, ok, err := r.FindAlbumByMBID(ctx, rg); err != nil || ok {
		t.Fatalf("before enrich: %v %v", ok, err)
	}
	if err := setAlbumMeta(ctx, r.db, id, map[string]any{"mb_id": rg}); err != nil {
		t.Fatal(err)
	}
	if it, ok, err := r.FindAlbumByMBID(ctx, rg); err != nil || !ok || it.ID != id {
		t.Fatalf("by mb_id: %+v %v %v", it, ok, err)
	}
	if _, ok, err := r.FindAlbumByMBID(ctx, ""); err != nil || ok {
		t.Fatalf("empty id: %v %v", ok, err)
	}
	// a missing album is not found
	if _, err := r.db.ExecContext(ctx, `UPDATE items SET missing_since='2026-01-01T00:00:00Z' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := r.FindAlbumByMBID(ctx, rg); err != nil || ok {
		t.Fatalf("missing album: %v %v", ok, err)
	}
}

func TestFindAlbumByMBIDFromTags(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	const rg = "11111111-2222-3333-4444-555555555555"
	id, err := r.UpsertAlbumTracks(ctx, "Кино", []TrackInput{{File: FileInput{Path: "/m/Кино/Ночь/01.flac", Size: 1, Mtime: 1},
		AlbumDir: "/m/Кино/Ночь", Album: "Ночь", Title: "Ночь", Track: 1, MBReleaseGroupID: rg}})
	if err != nil {
		t.Fatal(err)
	}
	if it, ok, err := r.FindAlbumByMBID(ctx, rg); err != nil || !ok || it.ID != id {
		t.Fatalf("by tag: %+v %v %v", it, ok, err)
	}
}

func TestAlbumFolderTracks(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	id := kinoAlbum(t, r)
	// a second album item in the same folder (an edition tag the scanner
	// kept apart) and an album elsewhere
	other, err := r.UpsertAlbumTracks(ctx, "Кино", []TrackInput{{File: FileInput{Path: "/m/Кино/Группа крови/05.flac", Size: 1, Mtime: 1},
		AlbumDir: "/m/Кино/Группа крови", Album: "Группа крови (Live)", Title: "Бошетунмай", Track: 5}})
	if err != nil || other == id {
		t.Fatalf("%d %v", other, err)
	}
	if _, err := r.UpsertAlbumTracks(ctx, "Кино", []TrackInput{{File: FileInput{Path: "/m/Кино/Группа крови 2/01.flac", Size: 1, Mtime: 1},
		AlbumDir: "/m/Кино/Группа крови 2", Album: "Группа крови", Title: "Чужая", Track: 1}}); err != nil {
		t.Fatal(err)
	}
	got, err := r.AlbumFolderTracks(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tr := range got {
		names = append(names, tr.Title)
	}
	if len(got) != 4 || names[3] != "Бошетунмай" {
		t.Fatalf("tracks %v", names)
	}
	if got, err := r.AlbumFolderTracks(ctx, 999999); err != nil || len(got) != 0 {
		t.Fatalf("unknown album %v %v", got, err)
	}
}
