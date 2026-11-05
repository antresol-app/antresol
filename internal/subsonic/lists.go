package subsonic

import (
	"errors"
	"sort"
	"strings"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
)

// albumListQuery maps getAlbumList(2) parameters to an AlbumQuery; a
// non-nil response is the error to send.
func (c *call) albumListQuery() (library.AlbumQuery, *response) {
	size, offset := c.pageParams("size", "offset", 10)
	q := library.AlbumQuery{UserID: c.u.ID, Limit: size, Offset: offset, MaxRating: c.limit()}
	switch t := c.p.Get("type"); t {
	case "":
		return q, c.missing("type")
	case "random":
		q.Sort = "random"
	case "newest":
		q.Sort = "added"
	case "frequent", "highest": // no ratings: the most played stand in for the best rated
		q.Sort = "frequent"
	case "recent":
		q.Sort = "recent"
	case "alphabeticalByName":
		q.Sort = "title"
	case "alphabeticalByArtist":
		q.Sort = "artist"
	case "starred":
		q.Starred = true
		q.Sort = "title"
	case "byYear":
		if c.p.Get("fromYear") == "" {
			return q, c.missing("fromYear")
		}
		if c.p.Get("toYear") == "" {
			return q, c.missing("toYear")
		}
		q.FromYear, q.ToYear = c.intParam("fromYear", 0), c.intParam("toYear", 0)
	case "byGenre":
		g := strings.TrimSpace(c.p.Get("genre"))
		if g == "" {
			return q, c.missing("genre")
		}
		q.Genre = g
		q.Sort = "title"
	default:
		return q, c.fail(codeGeneric, i18n.T(c.ctx, "Неизвестный тип списка: %s", t))
	}
	if q.Sort == "added" {
		q.Sort = "" // Albums' default: newest added first
	}
	return q, nil
}

func (c *call) albumListAlbums() ([]library.Album, *response) {
	q, fail := c.albumListQuery()
	if fail != nil {
		return nil, fail
	}
	if q.Limit == 0 {
		return []library.Album{}, nil
	}
	as, _, err := c.s.Library.Albums(c.ctx, q) // MaxRating applies before LIMIT/OFFSET
	if err != nil {
		return nil, c.internal("getAlbumList", err)
	}
	return as, nil
}

func (c *call) getAlbumList() *response {
	as, fail := c.albumListAlbums()
	if fail != nil {
		return fail
	}
	out := &albumList{Album: make([]child, 0, len(as))}
	for _, a := range as {
		out.Album = append(out.Album, c.albumDir(a))
	}
	resp := c.ok()
	resp.AlbumList = out
	return resp
}

func (c *call) getAlbumList2() *response {
	as, fail := c.albumListAlbums()
	if fail != nil {
		return fail
	}
	out := &albumList2{Album: make([]albumID3, 0, len(as))}
	for _, a := range as {
		out.Album = append(out.Album, c.albumID3(a))
	}
	resp := c.ok()
	resp.AlbumList2 = out
	return resp
}

func (c *call) getRandomSongs() *response {
	size, _ := c.pageParams("size", "", 10)
	if size == 0 {
		resp := c.ok()
		resp.RandomSongs = &songs{Song: []child{}}
		return resp
	}
	ids, err := c.s.Music.RandomTracks(c.ctx, size, c.p.Get("genre"), c.intParam("fromYear", 0), c.intParam("toYear", 0), c.limit())
	if err != nil {
		return c.internal("getRandomSongs", err)
	}
	tracks, err := c.s.Library.Tracks(c.ctx, ids)
	if err != nil {
		return c.internal("getRandomSongs", err)
	}
	ss, err := c.songs(tracks)
	if err != nil {
		return c.internal("getRandomSongs", err)
	}
	resp := c.ok()
	resp.RandomSongs = &songs{Song: ss}
	return resp
}

// searchResult holds what search2/search3 found.
type searchResult struct {
	artists []artistID3
	albums  []library.Album
	songs   []child
}

// search runs a search; an empty query ("" or a pair of quotes) lists
// everything page by page, which is how Amperfy and others sync.
func (c *call) search() (searchResult, *response) {
	var res searchResult
	if !c.p.Has("query") {
		return res, c.missing("query")
	}
	text := strings.TrimSpace(strings.Trim(strings.TrimSpace(c.p.Get("query")), `"`))
	arN, arOff := c.pageParams("artistCount", "artistOffset", 20)
	alN, alOff := c.pageParams("albumCount", "albumOffset", 20)
	soN, soOff := c.pageParams("songCount", "songOffset", 20)
	res.artists, res.albums, res.songs = []artistID3{}, []library.Album{}, []child{}
	if text == "" {
		return c.listAll(res, arN, arOff, alN, alOff, soN, soOff)
	}
	limit := c.limit()
	if arN > 0 {
		items, err := c.s.Library.SearchMusic(c.ctx, text, library.KindArtist, arN, arOff, limit)
		if err != nil {
			return res, c.internal("search", err)
		}
		for _, it := range items {
			row, err := c.s.Library.ArtistInfo(c.ctx, it)
			if err != nil {
				return res, c.internal("search", err)
			}
			res.artists = append(res.artists, c.artistID3(row))
		}
	}
	if alN > 0 {
		items, err := c.s.Library.SearchMusic(c.ctx, text, library.KindAlbum, alN, alOff, limit)
		if err != nil {
			return res, c.internal("search", err)
		}
		for _, it := range items {
			a, err := c.s.Library.AlbumByID(c.ctx, it.ID)
			if errors.Is(err, library.ErrNotFound) {
				continue
			}
			if err != nil {
				return res, c.internal("search", err)
			}
			res.albums = append(res.albums, a)
		}
	}
	if soN > 0 {
		items, err := c.s.Library.SearchMusic(c.ctx, text, library.KindTrack, soN, soOff, limit)
		if err != nil {
			return res, c.internal("search", err)
		}
		ids := []int64{}
		for _, it := range items {
			ids = append(ids, it.ID)
		}
		tracks, err := c.s.Library.Tracks(c.ctx, ids)
		if err != nil {
			return res, c.internal("search", err)
		}
		if res.songs, err = c.songs(tracks); err != nil {
			return res, c.internal("search", err)
		}
	}
	return res, nil
}

func (c *call) listAll(res searchResult, arN, arOff, alN, alOff, soN, soOff int) (searchResult, *response) {
	if arN > 0 {
		rows, err := c.s.Library.MusicArtists(c.ctx, arN, arOff, c.limit())
		if err != nil {
			return res, c.internal("search", err)
		}
		for _, a := range rows {
			res.artists = append(res.artists, c.artistID3(a))
		}
	}
	if alN > 0 {
		as, _, err := c.s.Library.Albums(c.ctx, library.AlbumQuery{Sort: "title", Limit: alN, Offset: alOff, MaxRating: c.limit()})
		if err != nil {
			return res, c.internal("search", err)
		}
		res.albums = as // MaxRating filtered them in SQL
	}
	if soN > 0 {
		tracks, err := c.s.Library.MusicSongs(c.ctx, soN, soOff, c.limit())
		if err != nil {
			return res, c.internal("search", err)
		}
		if res.songs, err = c.songs(tracks); err != nil {
			return res, c.internal("search", err)
		}
	}
	return res, nil
}

func (c *call) search2() *response {
	res, fail := c.search()
	if fail != nil {
		return fail
	}
	out := &searchResult2{Artist: res.artists, Album: make([]child, 0, len(res.albums)), Song: res.songs}
	for _, a := range res.albums {
		out.Album = append(out.Album, c.albumDir(a))
	}
	resp := c.ok()
	resp.SearchResult2 = out
	return resp
}

func (c *call) search3() *response {
	res, fail := c.search()
	if fail != nil {
		return fail
	}
	out := &searchResult3{Artist: res.artists, Album: make([]albumID3, 0, len(res.albums)), Song: res.songs}
	for _, a := range res.albums {
		out.Album = append(out.Album, c.albumID3(a))
	}
	resp := c.ok()
	resp.SearchResult3 = out
	return resp
}

// starredItems splits the profile's visible stars by kind, newest first.
func (c *call) starredItems() (searchResult, error) {
	res := searchResult{artists: []artistID3{}, albums: []library.Album{}, songs: []child{}}
	stars, err := c.starredAt()
	if err != nil {
		return res, err
	}
	ids := make([]int64, 0, len(stars))
	for id := range stars {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := stars[ids[i]], stars[ids[j]]
		if !a.Equal(b) {
			return a.After(b)
		}
		return ids[i] > ids[j]
	})
	var trackIDs []int64
	for _, id := range ids {
		it, err := c.item(id)
		if err != nil {
			return res, err
		}
		if it == nil {
			continue
		}
		switch it.Kind {
		case library.KindTrack:
			trackIDs = append(trackIDs, id)
		case library.KindAlbum:
			a, err := c.visibleAlbum(id)
			if err != nil {
				return res, err
			}
			if a != nil {
				res.albums = append(res.albums, *a)
			}
		case library.KindArtist:
			row, err := c.s.Library.ArtistInfo(c.ctx, *it)
			if err != nil {
				return res, err
			}
			res.artists = append(res.artists, c.artistID3(row))
		}
	}
	tracks, err := c.s.Library.Tracks(c.ctx, trackIDs)
	if err != nil {
		return res, err
	}
	res.songs, err = c.songs(tracks)
	return res, err
}

func (c *call) getStarred() *response {
	res, err := c.starredItems()
	if err != nil {
		return c.internal("getStarred", err)
	}
	out := &starred{Artist: res.artists, Album: make([]child, 0, len(res.albums)), Song: res.songs}
	for _, a := range res.albums {
		out.Album = append(out.Album, c.albumDir(a))
	}
	resp := c.ok()
	resp.Starred = out
	return resp
}

func (c *call) getStarred2() *response {
	res, err := c.starredItems()
	if err != nil {
		return c.internal("getStarred2", err)
	}
	out := &starred2{Artist: res.artists, Album: make([]albumID3, 0, len(res.albums)), Song: res.songs}
	for _, a := range res.albums {
		out.Album = append(out.Album, c.albumID3(a))
	}
	resp := c.ok()
	resp.Starred2 = out
	return resp
}
