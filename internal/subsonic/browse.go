package subsonic

import (
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/i18n"
	"github.com/antresol-app/antresol/internal/library"
	"github.com/antresol-app/antresol/internal/natsort"
)

// ignoredArticles are skipped when artists are put under a letter.
const ignoredArticles = "The"

func (c *call) getMusicFolders() *response {
	resp := c.ok()
	resp.MusicFolders = &musicFolders{MusicFolder: []musicFolder{{ID: musicFolderID, Name: i18n.T(c.ctx, "Музыка")}}}
	return resp
}

// indexName is the letter an artist is listed under: the first letter
// after an ignored article, upper case; "#" for anything else.
func indexName(name string) string {
	s := sortName(name)
	r, _ := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError || !unicode.IsLetter(r) {
		return "#"
	}
	if r = unicode.ToUpper(r); r == 'Ё' {
		r = 'Е' // Ё sorts with Е, not before А
	}
	return string(r)
}

// sortName drops a leading ignored article ("The Beatles" → "Beatles").
func sortName(name string) string {
	s := strings.TrimSpace(name)
	for _, a := range strings.Fields(ignoredArticles) {
		if len(s) > len(a)+1 && strings.EqualFold(s[:len(a)], a) && s[len(a)] == ' ' {
			return strings.TrimSpace(s[len(a)+1:])
		}
	}
	return s
}

// artistIndex groups the visible artists by letter: letters in order,
// "#" last; artists by name within a letter.
func (c *call) artistIndex() ([]index, int64, error) {
	rows, _, err := c.s.Library.Artists(c.ctx, 0, 0)
	if err != nil {
		return nil, 0, err
	}
	var last int64
	by := map[string][]artistID3{}
	names := map[string]string{}
	for _, a := range rows {
		ok, err := c.allowed(a.Item)
		if err != nil {
			return nil, 0, err
		}
		if !ok {
			continue
		}
		if ms := a.UpdatedAt.UnixMilli(); ms > last {
			last = ms
		}
		name := c.loc(a.Title) // «Разные исполнители» → Various Artists, indexed as shown
		k := indexName(name)
		by[k] = append(by[k], c.artistID3(a))
		names[sid(a.ID)] = sortName(name)
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if (keys[i] == "#") != (keys[j] == "#") {
			return keys[j] == "#"
		}
		return keys[i] < keys[j]
	})
	out := make([]index, 0, len(keys))
	for _, k := range keys {
		list := by[k]
		sort.SliceStable(list, func(i, j int) bool { return natsort.Less(names[list[i].ID], names[list[j].ID]) })
		out = append(out, index{Name: k, Artist: list})
	}
	return out, last, nil
}

func (c *call) getIndexes() *response {
	idx, last, err := c.artistIndex()
	if err != nil {
		return c.internal("getIndexes", err)
	}
	resp := c.ok()
	resp.Indexes = &indexes{LastModified: last, IgnoredArticles: ignoredArticles, Index: idx}
	return resp
}

func (c *call) getArtists() *response {
	idx, _, err := c.artistIndex()
	if err != nil {
		return c.internal("getArtists", err)
	}
	resp := c.ok()
	resp.Artists = &artistsID3{IgnoredArticles: ignoredArticles, Index: idx}
	return resp
}

func (c *call) getArtist() *response {
	v := c.p.Get("id")
	if v == "" {
		return c.missing("id")
	}
	id, ok := parseID(v, "ar-")
	if !ok {
		return c.fail(codeNotFound, "Артист не найден")
	}
	it, err := c.item(id, library.KindArtist)
	if err != nil {
		return c.internal("getArtist", err)
	}
	if it == nil {
		return c.fail(codeNotFound, "Артист не найден")
	}
	row, err := c.s.Library.ArtistInfo(c.ctx, *it)
	if err != nil {
		return c.internal("getArtist", err)
	}
	albums, _, err := c.s.Library.Albums(c.ctx, library.AlbumQuery{ArtistID: id, Sort: "year", MaxRating: c.limit()})
	if err != nil {
		return c.internal("getArtist", err)
	}
	if albums, err = c.visibleAlbums(albums); err != nil {
		return c.internal("getArtist", err)
	}
	out := &artistWithAlbums{artistID3: c.artistID3(row), Album: make([]albumID3, 0, len(albums))}
	out.AlbumCount = len(albums)
	for _, a := range albums {
		out.Album = append(out.Album, c.albumID3(a))
	}
	resp := c.ok()
	resp.Artist = out
	return resp
}

// visibleAlbum loads an album the profile may see; nil when not.
func (c *call) visibleAlbum(id int64) (*library.Album, error) {
	a, err := c.s.Library.AlbumByID(c.ctx, id)
	if errors.Is(err, library.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ok, err := c.allowed(a.Item)
	if err != nil || !ok {
		return nil, err
	}
	return &a, nil
}

func (c *call) getAlbum() *response {
	v := c.p.Get("id")
	if v == "" {
		return c.missing("id")
	}
	id, ok := parseID(v, "al-")
	if !ok {
		return c.fail(codeNotFound, "Альбом не найден")
	}
	a, err := c.visibleAlbum(id)
	if err != nil {
		return c.internal("getAlbum", err)
	}
	if a == nil {
		return c.fail(codeNotFound, "Альбом не найден")
	}
	tracks, err := c.s.Library.AlbumTracks(c.ctx, id)
	if err != nil {
		return c.internal("getAlbum", err)
	}
	ss, err := c.songs(tracks)
	if err != nil {
		return c.internal("getAlbum", err)
	}
	resp := c.ok()
	resp.Album = &albumWithSongs{albumID3: c.albumID3(*a), Song: ss}
	return resp
}

func (c *call) getSong() *response {
	v := c.p.Get("id")
	if v == "" {
		return c.missing("id")
	}
	id, ok := parseID(v, "")
	if !ok {
		return c.fail(codeNotFound, msgSongMissing)
	}
	tr, err := c.track(id)
	if err != nil {
		return c.internal("getSong", err)
	}
	if tr == nil {
		return c.fail(codeNotFound, msgSongMissing)
	}
	ss, err := c.songs([]library.Track{*tr})
	if err != nil {
		return c.internal("getSong", err)
	}
	if len(ss) == 0 {
		return c.fail(codeNotFound, msgSongMissing)
	}
	resp := c.ok()
	resp.Song = &ss[0]
	return resp
}

func (c *call) getGenres() *response {
	gs, err := c.s.Music.Genres(c.ctx, c.limit())
	if err != nil {
		return c.internal("getGenres", err)
	}
	out := make([]genre, 0, len(gs))
	for _, g := range gs {
		out = append(out, genre{Value: g.Name, AlbumCount: g.Albums, SongCount: g.Songs})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Value) < strings.ToLower(out[j].Value) })
	resp := c.ok()
	resp.Genres = &genres{Genre: out}
	return resp
}

// getMusicDirectory is folder browsing over the same ids as getIndexes
// and getArtists: an artist lists its albums as directories, an album its
// songs.
func (c *call) getMusicDirectory() *response {
	v := c.p.Get("id")
	if v == "" {
		return c.missing("id")
	}
	id, ok := parseID(strings.TrimPrefix(strings.TrimPrefix(v, "ar-"), "al-"), "")
	if !ok {
		return c.fail(codeNotFound, "Папка не найдена")
	}
	it, err := c.item(id, library.KindArtist, library.KindAlbum)
	if err != nil {
		return c.internal("getMusicDirectory", err)
	}
	if it == nil {
		return c.fail(codeNotFound, "Папка не найдена")
	}
	dir := &directory{ID: sid(it.ID), Name: c.loc(it.Title), Child: []child{}}
	if it.Kind == library.KindArtist {
		albums, _, err := c.s.Library.Albums(c.ctx, library.AlbumQuery{ArtistID: id, Sort: "year", MaxRating: c.limit()})
		if err != nil {
			return c.internal("getMusicDirectory", err)
		}
		for _, a := range albums {
			dir.Child = append(dir.Child, c.albumDir(a))
		}
	} else {
		dir.Parent = idOrEmpty(it.ParentID)
		tracks, err := c.s.Library.AlbumTracks(c.ctx, id)
		if err != nil {
			return c.internal("getMusicDirectory", err)
		}
		if dir.Child, err = c.songs(tracks); err != nil {
			return c.internal("getMusicDirectory", err)
		}
	}
	resp := c.ok()
	resp.Directory = dir
	return resp
}
