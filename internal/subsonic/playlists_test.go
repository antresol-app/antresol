package subsonic

import (
	"context"
	"testing"

	"github.com/antresol-app/antresol/internal/auth"
)

func entryIDs(pl map[string]any) []string {
	var out []string
	es, _ := pl["entry"].([]any)
	for _, e := range es {
		out = append(out, e.(map[string]any)["id"].(string))
	}
	return out
}

func TestPlaylistsRoundTrip(t *testing.T) {
	e := newEnv(t)
	_, tr := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Раз"}, tTrack{2, "Два"}, tTrack{3, "Три"})
	pl := e.mustOK(e.call("createPlaylist", "name", "Дорога", "songId", itoa(tr[0]), "songId", itoa(tr[1])))["playlist"].(map[string]any)
	id := pl["id"].(string)
	if pl["name"] != "Дорога" || pl["owner"] != "Папа" || pl["songCount"] != 2.0 || len(entryIDs(pl)) != 2 {
		t.Fatalf("created %v", pl)
	}
	lists := e.mustOK(e.call("getPlaylists"))["playlists"].(map[string]any)["playlist"].([]any)
	if len(lists) != 1 || lists[0].(map[string]any)["id"] != id {
		t.Fatalf("playlists %v", lists)
	}
	e.mustOK(e.call("updatePlaylist", "playlistId", id, "name", "Домой", "songIdToAdd", itoa(tr[2]), "songIndexToRemove", "0"))
	pl = e.mustOK(e.call("getPlaylist", "id", id))["playlist"].(map[string]any)
	if got := entryIDs(pl); pl["name"] != "Домой" || len(got) != 2 || got[0] != itoa(tr[1]) || got[1] != itoa(tr[2]) {
		t.Fatalf("updated %v", pl)
	}
	if code, _ := errCode(e.call("updatePlaylist", "playlistId", id, "songIndexToRemove", "5")); code != 0 {
		t.Fatalf("index past the end: %d", code)
	}
	if code, _ := errCode(e.call("createPlaylist", "name", "X", "songId", "99999")); code != 70 {
		t.Fatalf("unknown song: %d", code)
	}
	// createPlaylist with playlistId replaces the songs
	pl = e.mustOK(e.call("createPlaylist", "playlistId", id, "songId", itoa(tr[0])))["playlist"].(map[string]any)
	if got := entryIDs(pl); len(got) != 1 || got[0] != itoa(tr[0]) {
		t.Fatalf("replaced %v", pl)
	}
	// someone else's playlist is not found
	ctx := context.Background()
	other, _ := e.users.CreateUser(ctx, auth.NewUser{Name: "Мама", Password: "secret"})
	app, _ := e.users.SetAppPassword(ctx, other.ID)
	e.admin, e.app = other, app
	if code, _ := errCode(e.call("getPlaylist", "id", id)); code != 70 {
		t.Fatalf("other user's playlist: %d", code)
	}
	if code, _ := errCode(e.call("deletePlaylist", "id", id)); code != 70 {
		t.Fatalf("other user deletes: %d", code)
	}
}

func TestPlaylistRemoveSkipsHidden(t *testing.T) {
	e := newEnv(t)
	_, tr := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Раз"}, tTrack{2, "Два"}, tTrack{3, "Три"})
	id := e.mustOK(e.call("createPlaylist", "name", "Дорога", "songId", itoa(tr[0]), "songId", itoa(tr[1]), "songId", itoa(tr[2])))["playlist"].(map[string]any)["id"].(string)
	if _, err := e.db.Exec(`UPDATE items SET missing_since='2024-01-01T00:00:00Z' WHERE id=?`, tr[0]); err != nil {
		t.Fatal(err)
	}
	// the client sees [Два, Три]; index 0 is Два
	e.mustOK(e.call("updatePlaylist", "playlistId", id, "songIndexToRemove", "0"))
	if _, err := e.db.Exec(`UPDATE items SET missing_since=NULL WHERE id=?`, tr[0]); err != nil {
		t.Fatal(err)
	}
	pl := e.mustOK(e.call("getPlaylist", "id", id))["playlist"].(map[string]any)
	if got := entryIDs(pl); len(got) != 2 || got[0] != itoa(tr[0]) || got[1] != itoa(tr[2]) {
		t.Fatalf("hidden entry must stay, the visible one go: %v", got)
	}
	e.mustOK(e.call("deletePlaylist", "id", id))
	if code, _ := errCode(e.call("getPlaylist", "id", id)); code != 70 {
		t.Fatalf("deleted: %d", code)
	}
}

func TestStarAndGetStarred2(t *testing.T) {
	e := newEnv(t)
	al, tr := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Раз"})
	art := e.mustOK(e.call("getAlbum", "id", itoa(al)))["album"].(map[string]any)["artistId"].(string)
	e.mustOK(e.call("star", "id", itoa(tr[0]), "albumId", itoa(al), "artistId", art))
	s := e.mustOK(e.call("getStarred2"))["starred2"].(map[string]any)
	if len(s["song"].([]any)) != 1 || len(s["album"].([]any)) != 1 || len(s["artist"].([]any)) != 1 {
		t.Fatalf("starred2 %v", s)
	}
	if song := s["song"].([]any)[0].(map[string]any); song["starred"] == nil || song["id"] != itoa(tr[0]) {
		t.Fatalf("song %v", song)
	}
	e.mustOK(e.call("getStarred"))
	if code, _ := errCode(e.call("star", "albumId", itoa(tr[0]))); code != 70 {
		t.Fatalf("a track as albumId: %d", code)
	}
	e.mustOK(e.call("unstar", "id", itoa(tr[0])))
	s = e.mustOK(e.call("getStarred2"))["starred2"].(map[string]any)
	if len(s["song"].([]any)) != 0 || len(s["album"].([]any)) != 1 {
		t.Fatalf("after unstar %v", s)
	}
	al2 := e.mustOK(e.call("getAlbumList2", "type", "starred"))["albumList2"].(map[string]any)["album"].([]any)
	if len(al2) != 1 {
		t.Fatalf("starred list %v", al2)
	}
}

func TestKidSeesUnratedHidesRated(t *testing.T) {
	e := newEnv(t)
	ok, _ := e.album("Кино", "Группа крови", 1988, "", tTrack{1, "Раз"})
	bad, btr := e.album("Взрослые", "Только 18+", 2000, "", tTrack{1, "Нельзя"})
	if _, err := e.db.Exec(`UPDATE items SET rating='18+' WHERE id=(SELECT parent_id FROM items WHERE id=?)`, bad); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	kid, _ := e.users.CreateUser(ctx, auth.NewUser{Name: "Вася", IsKid: true})
	app, _ := e.users.SetAppPassword(ctx, kid.ID)
	e.admin, e.app = kid, app
	e.mustOK(e.call("getAlbum", "id", itoa(ok)))
	if code, _ := errCode(e.call("getAlbum", "id", itoa(bad))); code != 70 {
		t.Fatalf("rated album: %d", code)
	}
	if code, _ := errCode(e.call("getSong", "id", itoa(btr[0]))); code != 70 {
		t.Fatalf("rated song: %d", code)
	}
	list := e.mustOK(e.call("getAlbumList2", "type", "alphabeticalByName"))["albumList2"].(map[string]any)["album"].([]any)
	if len(list) != 1 {
		t.Fatalf("list %v", list)
	}
	idx := e.mustOK(e.call("getArtists"))["artists"].(map[string]any)["index"].([]any)
	if len(idx) != 1 {
		t.Fatalf("artists %v", idx)
	}
	u := e.mustOK(e.call("getUser", "username", "Вася"))["user"].(map[string]any)
	if u["adminRole"] != false || u["streamRole"] != true || u["downloadRole"] != true || u["playlistRole"] != true {
		t.Fatalf("user %v", u)
	}
	if code, _ := errCode(e.call("getUser", "username", "Папа")); code != 50 {
		t.Fatalf("kid looks up another profile: %d", code)
	}
}
