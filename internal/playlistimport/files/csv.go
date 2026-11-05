package files

import (
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/playlistimport"
)

// CSV columns, by their header (compared in lower case): the universal
// format «artist,title,album,isrc,playlist,position,dateAdded» (an empty
// playlist: a liked song), Exportify and TuneMyMusic.
const (
	colArtist = iota
	colTitle
	colAlbum
	colAlbumArtist
	colISRC
	colPlaylist
	colPosition
	colAdded
	colDuration
	colKey
	colCount
)

var csvAliases = map[string]int{
	"artist": colArtist, "artist name(s)": colArtist, "artist name": colArtist, "исполнитель": colArtist,
	"title": colTitle, "track name": colTitle, "название": colTitle,
	"album": colAlbum, "album name": colAlbum, "альбом": colAlbum,
	"album artist name(s)": colAlbumArtist, "album artist": colAlbumArtist,
	"isrc":     colISRC,
	"playlist": colPlaylist, "playlist name": colPlaylist, "плейлист": colPlaylist,
	"position":  colPosition,
	"dateadded": colAdded, "added at": colAdded,
	"track duration (ms)": colDuration, "duration (ms)": colDuration, "durationms": colDuration,
	"track uri": colKey,
}

// likedFiles name a file of liked songs (its name, in norm form, with «_»
// as a space: Exportify saves «liked_songs.csv»).
var likedFiles = []string{"liked songs", "liked", "понравившиеся"}

type csvRow struct {
	key      string
	position int // 0: none
	seq      int
}

// parseCSV reads a table of songs; stem (the file name without its
// extension) names the playlist of a table without a playlist column.
func parseCSV(stem string, text []byte) (playlistimport.Snapshot, error) {
	r := csv.NewReader(bytes.NewReader(text))
	r.Comma = sniffComma(text)
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return playlistimport.Snapshot{}, ErrUnreadable
	}
	cols := make([]int, colCount)
	for i := range cols {
		cols[i] = -1
	}
	exportify := false
	for i, h := range header {
		h = strings.ToLower(strings.TrimSpace(h))
		if c, ok := csvAliases[h]; ok && cols[c] < 0 {
			cols[c] = i
			exportify = exportify || h == "artist name(s)"
		}
	}
	if cols[colArtist] < 0 || cols[colTitle] < 0 {
		return playlistimport.Snapshot{}, ErrNoColumns
	}

	b := newBuilder(playlistimport.ServiceFile)
	byPlaylist := map[string][]csvRow{} // name → its rows
	var order []string                  // playlist names, first seen first
	fileName := strings.TrimSpace(stem)
	if fileName == "" {
		fileName = "CSV" // «.csv»: still a playlist, not liked songs
	}
	fileLiked := slices.Contains(likedFiles, norm(strings.ReplaceAll(stem, "_", " ")))
	for seq := 0; ; seq++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		var pe *csv.ParseError
		if errors.As(err, &pe) && errors.Is(pe.Err, csv.ErrFieldCount) {
			err = nil
		}
		if err != nil {
			return playlistimport.Snapshot{}, ErrUnreadable
		}
		get := func(c int) string {
			if i := cols[c]; i >= 0 && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		t := playlistimport.SnapTrack{Key: get(colKey), Artist: get(colArtist), Title: get(colTitle), Album: get(colAlbum),
			AlbumArtist: get(colAlbumArtist), ISRC: get(colISRC), AddedAt: parseDate(get(colAdded))}
		if exportify {
			t.Artist, t.AlbumArtist = firstArtist(t.Artist), firstArtist(t.AlbumArtist)
		}
		if t.Artist == "" || t.Title == "" {
			continue
		}
		t.DurationMs, _ = strconv.ParseInt(get(colDuration), 10, 64)
		playlist := fileName
		if cols[colPlaylist] >= 0 {
			playlist = get(colPlaylist)
		}
		t.Liked = playlist == "" || (cols[colPlaylist] < 0 && fileLiked)
		key, err := b.add(t)
		if err != nil {
			return playlistimport.Snapshot{}, err
		}
		if t.Liked {
			continue
		}
		if _, ok := byPlaylist[playlist]; !ok {
			order = append(order, playlist)
		}
		pos, _ := strconv.Atoi(get(colPosition))
		byPlaylist[playlist] = append(byPlaylist[playlist], csvRow{key: key, position: max(pos, 0), seq: seq})
	}
	for _, name := range order {
		rows := byPlaylist[name]
		// by position; rows without one keep their place after those with one
		slices.SortStableFunc(rows, func(a, b csvRow) int {
			switch {
			case a.position == 0 && b.position == 0:
				return 0
			case a.position == 0:
				return 1
			case b.position == 0:
				return -1
			}
			return a.position - b.position
		})
		p, err := b.playlist("", name)
		if err != nil {
			return playlistimport.Snapshot{}, err
		}
		for _, row := range rows {
			if err := b.entry(p, row.key); err != nil {
				return playlistimport.Snapshot{}, err
			}
		}
	}
	return b.snap, nil
}

// sniffComma picks the separator of the header line: «,», «;» (Excel in
// Russia) or a tab, the most frequent outside quotes.
func sniffComma(text []byte) rune {
	line, _, _ := bytes.Cut(text, []byte("\n"))
	counts := map[rune]int{}
	quoted := false
	for _, c := range string(line) {
		switch {
		case c == '"':
			quoted = !quoted
		case !quoted && (c == ',' || c == ';' || c == '\t'):
			counts[c]++
		}
	}
	best := ','
	for _, c := range []rune{';', '\t'} {
		if counts[c] > counts[best] {
			best = c
		}
	}
	return best
}

// firstArtist is the first of Exportify's artists «A, B» (a comma within
// a name is escaped: «AC\, DC»).
func firstArtist(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s) && s[i+1] == ',':
			b.WriteByte(',')
			i++
		case s[i] == ',':
			return strings.TrimSpace(b.String())
		default:
			b.WriteByte(s[i])
		}
	}
	return strings.TrimSpace(b.String())
}

// parseDate reads a date added (RFC 3339, «2006-01-02 15:04:05», a bare
// date, Takeout's «… UTC»); nil when empty or unknown.
func parseDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05 MST", "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	return nil
}
