package subsonic

import (
	"errors"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/music"
)

// maxPlaylist bounds a playlist, as the web API does.
const maxPlaylist = 5000

var (
	errPlaylistPos  = errors.New("bad playlist position")
	errTooManySongs = errors.New("playlist too long")
)

func playlistID(id int64) string { return "pl-" + sid(id) }

func (c *call) playlistOf(p music.Playlist) playlist {
	out := playlist{ID: playlistID(p.ID), Name: p.Name, Owner: c.u.Name, SongCount: p.TrackCount,
		Duration: secs(p.DurationMs), Created: stamp(p.UpdatedAt), Changed: stamp(p.UpdatedAt)}
	if len(p.CoverAlbums) > 0 {
		out.CoverArt = playlistID(p.ID)
	}
	return out
}

// ownName checks the optional username parameter: only one's own lists.
func (c *call) ownName() *response {
	if n := c.p.Get("username"); n != "" && !strings.EqualFold(strings.TrimSpace(n), strings.TrimSpace(c.u.Name)) {
		return c.fail(codeForbidden, "Можно смотреть только свои плейлисты")
	}
	return nil
}

func (c *call) getPlaylists() *response {
	if fail := c.ownName(); fail != nil {
		return fail
	}
	ps, err := c.s.Music.Playlists(c.ctx, c.u.ID)
	if err != nil {
		return c.internal("getPlaylists", err)
	}
	out := &playlists{Playlist: make([]playlist, 0, len(ps))}
	for _, p := range ps {
		out.Playlist = append(out.Playlist, c.playlistOf(p))
	}
	resp := c.ok()
	resp.Playlists = out
	return resp
}

// visiblePositions returns the stored positions of the entries the
// profile sees (the Subsonic list), and their songs.
func (c *call) visiblePositions(ids []int64) ([]int, []child, error) {
	tracks, err := c.s.Library.Tracks(c.ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	by := make(map[int64]library.Track, len(tracks))
	for _, t := range tracks {
		by[t.ID] = t
	}
	var pos []int
	var vis []library.Track
	for i, id := range ids {
		t, ok := by[id]
		if !ok {
			continue
		}
		al, err := c.album(t.AlbumID)
		if err != nil {
			return nil, nil, err
		}
		if al != nil {
			pos = append(pos, i)
			vis = append(vis, t)
		}
	}
	ss, err := c.songs(vis)
	return pos, ss, err
}

func (c *call) playlistResponse(id int64) *response {
	p, ids, err := c.s.Music.Playlist(c.ctx, c.u.ID, id)
	if errors.Is(err, music.ErrNotFound) {
		return c.fail(codeNotFound, "Плейлист не найден")
	}
	if err != nil {
		return c.internal("getPlaylist", err)
	}
	_, ss, err := c.visiblePositions(ids)
	if err != nil {
		return c.internal("getPlaylist", err)
	}
	out := &playlistWithSongs{playlist: c.playlistOf(p), Entry: ss}
	out.SongCount = len(ss)
	resp := c.ok()
	resp.Playlist = out
	return resp
}

func (c *call) getPlaylist() *response {
	v := c.p.Get("id")
	if v == "" {
		return c.missing("id")
	}
	id, ok := parseID(v, "pl-")
	if !ok {
		return c.fail(codeNotFound, "Плейлист не найден")
	}
	return c.playlistResponse(id)
}

// songIDs reads a repeated song id parameter; each must be a visible
// track. A non-nil response is the error.
func (c *call) songIDs(name string) ([]int64, *response) {
	vals := c.p[name]
	if len(vals) > maxPlaylist {
		return nil, c.fail(codeGeneric, "Слишком много песен")
	}
	ids := make([]int64, 0, len(vals))
	for _, v := range vals {
		id, ok := parseID(v, "")
		if !ok {
			return nil, c.fail(codeNotFound, msgSongMissing)
		}
		ids = append(ids, id)
	}
	tracks, err := c.s.Library.Tracks(c.ctx, ids)
	if err != nil {
		return nil, c.internal("playlist songs", err)
	}
	if len(tracks) != len(ids) {
		return nil, c.fail(codeNotFound, msgSongMissing)
	}
	for _, t := range tracks {
		al, err := c.album(t.AlbumID)
		if err != nil {
			return nil, c.internal("playlist songs", err)
		}
		if al == nil {
			return nil, c.fail(codeNotFound, msgSongMissing)
		}
	}
	return ids, nil
}

func (c *call) editErr(what string, err error) *response {
	switch {
	case errors.Is(err, music.ErrNotFound):
		return c.fail(codeNotFound, "Плейлист не найден")
	case errors.Is(err, errPlaylistPos):
		return c.fail(codeGeneric, "Неверная позиция в плейлисте")
	case errors.Is(err, errTooManySongs):
		return c.fail(codeGeneric, "Слишком много песен")
	}
	return c.internal(what, err)
}

// createPlaylist makes a playlist of songId…, or with playlistId replaces
// that playlist's songs (entries the profile cannot see stay; the new
// songs take the visible slots, extra ones go to the end).
func (c *call) createPlaylist() *response {
	ids, fail := c.songIDs("songId")
	if fail != nil {
		return fail
	}
	name := strings.TrimSpace(c.p.Get("name"))
	if v := c.p.Get("playlistId"); v != "" {
		id, ok := parseID(v, "pl-")
		if !ok {
			return c.fail(codeNotFound, "Плейлист не найден")
		}
		var rename *string
		if name != "" {
			rename = &name
		}
		err := c.s.Music.EditPlaylist(c.ctx, c.u.ID, id, rename, func(cur []int64) ([]int64, error) {
			pos, _, err := c.visiblePositions(cur)
			if err != nil {
				return nil, err
			}
			next := replaceVisible(cur, pos, ids)
			if len(next) > maxPlaylist {
				return nil, errTooManySongs
			}
			return next, nil
		})
		if err != nil {
			return c.editErr("createPlaylist", err)
		}
		return c.playlistResponse(id)
	}
	if name == "" {
		return c.missing("name")
	}
	p, err := c.s.Music.CreatePlaylist(c.ctx, c.u.ID, name, ids)
	if err != nil {
		return c.internal("createPlaylist", err)
	}
	return c.playlistResponse(p.ID)
}

// replaceVisible puts ids into the visible slots pos of cur in order,
// drops visible slots left over and appends ids left over; hidden entries
// keep their places.
func replaceVisible(cur []int64, pos []int, ids []int64) []int64 {
	visible := make(map[int]bool, len(pos))
	for _, p := range pos {
		visible[p] = true
	}
	next := make([]int64, 0, len(cur)+len(ids))
	k := 0
	for i, v := range cur {
		switch {
		case !visible[i]:
			next = append(next, v)
		case k < len(ids):
			next = append(next, ids[k])
			k++
		}
	}
	return append(next, ids[k:]...)
}

// updatePlaylist renames (name), appends songIdToAdd… and removes
// songIndexToRemove… (positions in the list getPlaylist showed).
func (c *call) updatePlaylist() *response {
	v := c.p.Get("playlistId")
	if v == "" {
		return c.missing("playlistId")
	}
	id, ok := parseID(v, "pl-")
	if !ok {
		return c.fail(codeNotFound, "Плейлист не найден")
	}
	add, fail := c.songIDs("songIdToAdd")
	if fail != nil {
		return fail
	}
	var remove []int
	for _, s := range c.p["songIndexToRemove"] {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return c.fail(codeGeneric, "Неверная позиция в плейлисте")
		}
		remove = append(remove, n)
	}
	var rename *string
	if c.p.Has("name") {
		n := strings.TrimSpace(c.p.Get("name"))
		if n == "" {
			return c.fail(codeGeneric, "Введите название плейлиста")
		}
		rename = &n
	}
	var edit func([]int64) ([]int64, error)
	if len(add) > 0 || len(remove) > 0 {
		edit = func(cur []int64) ([]int64, error) {
			pos, _, err := c.visiblePositions(cur)
			if err != nil {
				return nil, err
			}
			drop := map[int]bool{}
			for _, n := range remove {
				if n >= len(pos) {
					return nil, errPlaylistPos
				}
				drop[pos[n]] = true
			}
			next := make([]int64, 0, len(cur)+len(add))
			for i, v := range cur {
				if !drop[i] {
					next = append(next, v)
				}
			}
			next = append(next, add...)
			if len(next) > maxPlaylist {
				return nil, errTooManySongs
			}
			return next, nil
		}
	}
	if err := c.s.Music.EditPlaylist(c.ctx, c.u.ID, id, rename, edit); err != nil {
		return c.editErr("updatePlaylist", err)
	}
	return c.ok()
}

func (c *call) deletePlaylist() *response {
	v := c.p.Get("id")
	if v == "" {
		return c.missing("id")
	}
	id, ok := parseID(v, "pl-")
	if !ok {
		return c.fail(codeNotFound, "Плейлист не найден")
	}
	if err := c.s.Music.DeletePlaylist(c.ctx, c.u.ID, id); errors.Is(err, music.ErrNotFound) {
		return c.fail(codeNotFound, "Плейлист не найден")
	} else if err != nil {
		return c.internal("deletePlaylist", err)
	}
	return c.ok()
}
