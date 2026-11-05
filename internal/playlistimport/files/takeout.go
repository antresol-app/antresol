package files

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/antresol-app/antresol/internal/playlistimport"
)

// Zip bounds: a compressed CSV may claim any size, so each is read through
// a limit, as is the sum of them (a zip bomb stops there).
const (
	maxZipEntries = 100_000
	maxZipFile    = 32 << 20
	maxZipTotal   = 256 << 20
)

// parseTakeout reads a Google Takeout of YouTube Music: «…/music library
// songs.csv» (the library: liked songs, keyed by video id) and each
// «…/playlists/<name>-videos.csv» (a playlist). Folder names are
// localized, so only those two names are matched. A playlist video not in
// the library stays an entry without a title (Unresolved) for the YouTube
// connector to name.
func parseTakeout(data []byte) (playlistimport.Snapshot, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil || len(z.File) > maxZipEntries {
		return playlistimport.Snapshot{}, ErrUnreadable
	}
	var library *zip.File
	var playlists []*zip.File
	for _, f := range z.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		lower := strings.ToLower(name)
		dir := strings.ToLower(path.Base(path.Dir(name)))
		switch {
		case f.FileInfo().IsDir():
		case strings.HasSuffix(lower, "music library songs.csv"):
			library = f
		case (dir == "playlists" || dir == "плейлисты") && strings.HasSuffix(lower, ".csv"):
			playlists = append(playlists, f)
		}
	}
	budget := int64(maxZipTotal)
	if library == nil && len(playlists) == 0 {
		return parseZipCSVs(z, &budget)
	}
	b := newBuilder(playlistimport.ServiceYouTube)
	if library != nil {
		rows, cols, err := readZipCSV(library, &budget, []string{"video id"}, map[string][]string{
			"title": {"song title"}, "album": {"album title"}, "artist": {"artist name", "artist names", "artist name 1"},
		})
		if errors.Is(err, errNoHeader) {
			err = ErrUnreadable
		}
		if err != nil {
			return playlistimport.Snapshot{}, err
		}
		for _, r := range rows {
			if r[0] == "" {
				continue
			}
			if _, err := b.add(playlistimport.SnapTrack{Key: r[0], Title: r[cols["title"]], Album: r[cols["album"]],
				Artist: r[cols["artist"]], Liked: true}); err != nil {
				return playlistimport.Snapshot{}, err
			}
		}
	}
	for _, f := range playlists {
		rows, _, err := readZipCSV(f, &budget, []string{"video id"}, nil)
		if errors.Is(err, errNoHeader) {
			continue // playlists.csv, the index of playlists, has no videos
		}
		if err != nil {
			return playlistimport.Snapshot{}, err
		}
		name := strings.TrimSuffix(path.Base(strings.ReplaceAll(f.Name, `\`, "/")), path.Ext(f.Name))
		for _, suffix := range []string{" videos", "-videos"} {
			if len(name) > len(suffix) && strings.EqualFold(name[len(name)-len(suffix):], suffix) {
				name = name[:len(name)-len(suffix)]
			}
		}
		p, err := b.playlist("", strings.TrimSpace(name))
		if err != nil {
			return playlistimport.Snapshot{}, err
		}
		for _, r := range rows {
			if r[0] == "" {
				continue
			}
			if _, known := b.at[r[0]]; !known {
				if _, err := b.add(playlistimport.SnapTrack{Key: r[0]}); err != nil { // unresolved
					return playlistimport.Snapshot{}, err
				}
			}
			if err := b.entry(p, r[0]); err != nil {
				return playlistimport.Snapshot{}, err
			}
		}
	}
	if len(b.snap.Tracks) == 0 {
		return playlistimport.Snapshot{}, ErrUnreadable
	}
	return b.snap, nil
}

var errNoHeader = errors.New("takeout: no header")

// readZipCSV reads a CSV of the zip from its header row, the first row
// with the columns of first (older Takeouts put a block of playlist
// details above it). Each row returned has first's columns, then the
// optional columns, which cols indexes by name (an absent one reads as
// an empty column).
func readZipCSV(f *zip.File, budget *int64, first []string, optional map[string][]string) (rows [][]string, cols map[string]int, err error) {
	text, err := readZipText(f, budget)
	if err != nil {
		return nil, nil, err
	}
	r := csv.NewReader(bytes.NewReader(text))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	var at []int // column of each wanted field
	names := []string{}
	for n := range optional {
		names = append(names, n)
	}
	slices.Sort(names)
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, ErrUnreadable
		}
		if at == nil {
			header := make([]string, len(rec))
			for i, h := range rec {
				header[i] = strings.ToLower(strings.TrimSpace(h))
			}
			idx := make([]int, 0, len(first)+len(names))
			for _, c := range first {
				i := slices.Index(header, c)
				if i < 0 {
					break
				}
				idx = append(idx, i)
			}
			if len(idx) < len(first) {
				continue // not the header yet
			}
			cols = map[string]int{}
			for _, n := range names {
				i := -1
				for _, alias := range optional[n] {
					if i = slices.Index(header, alias); i >= 0 {
						break
					}
				}
				cols[n] = len(idx)
				idx = append(idx, i)
			}
			at = idx
			continue
		}
		row := make([]string, len(at))
		for j, i := range at {
			if i >= 0 && i < len(rec) {
				row[j] = strings.TrimSpace(rec[i])
			}
		}
		if len(rows) >= maxEntries {
			return nil, nil, playlistimport.ErrTooLarge
		}
		rows = append(rows, row)
	}
	if at == nil {
		return nil, nil, errNoHeader
	}
	return rows, cols, nil
}

// readZipText reads a text file of the zip through the per-file limit and
// the zip's budget (the size it claims is not trusted).
func readZipText(f *zip.File, budget *int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, ErrUnreadable
	}
	defer rc.Close()
	limit := min(int64(maxZipFile), *budget)
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, ErrUnreadable
	}
	if int64(len(data)) > limit {
		return nil, playlistimport.ErrTooLarge
	}
	*budget -= int64(len(data))
	text, ok := decodeText(data)
	if !ok {
		return nil, ErrUnreadable
	}
	return text, nil
}

// parseZipCSVs reads a zip of CSV files that is not a Takeout (Exportify's
// «Export All»): each CSV as if uploaded on its own, merged; one without
// artist and title columns is skipped.
func parseZipCSVs(z *zip.Reader, budget *int64) (playlistimport.Snapshot, error) {
	m := NewMerger()
	for _, f := range z.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if f.FileInfo().IsDir() || strings.HasPrefix(name, "__MACOSX/") || !strings.EqualFold(path.Ext(name), ".csv") {
			continue
		}
		text, err := readZipText(f, budget)
		if err != nil {
			return playlistimport.Snapshot{}, err
		}
		base := path.Base(name)
		s, err := parseCSV(strings.TrimSpace(strings.TrimSuffix(base, path.Ext(base))), text)
		if errors.Is(err, ErrNoColumns) {
			continue
		}
		if err != nil {
			return playlistimport.Snapshot{}, err
		}
		if err := m.Add(s); err != nil {
			return playlistimport.Snapshot{}, err
		}
	}
	return m.Snapshot(), nil
}

// TakeoutResolver names YouTube videos a Takeout gives only by id
// (connect.YouTubeResolver: YouTube's oEmbed, or videos.list with a
// token): the songs it could name, by id; the rest stay unknown.
type TakeoutResolver interface {
	Resolve(ctx context.Context, ids []string) (map[string]playlistimport.SnapTrack, error)
}

// Resolve names a snapshot's songs known only by an id (Unresolved) with
// the resolver, after every uploaded file was merged (a split Takeout
// names many of its own videos). The snapshot comes back with the names
// filled in (the input is not changed); a resolver error is returned with
// whatever it named before it.
func Resolve(ctx context.Context, s playlistimport.Snapshot, r TakeoutResolver) (playlistimport.Snapshot, error) {
	var ids []string
	for _, t := range s.Tracks {
		if strings.TrimSpace(t.Title) == "" && t.Key != "" {
			ids = append(ids, t.Key)
		}
	}
	if len(ids) == 0 || r == nil {
		return s, nil
	}
	named, err := r.Resolve(ctx, ids)
	if len(named) > 0 {
		s.Tracks = slices.Clone(s.Tracks)
		for i, t := range s.Tracks {
			n, ok := named[t.Key]
			if !ok || strings.TrimSpace(t.Title) != "" || strings.TrimSpace(n.Title) == "" {
				continue
			}
			s.Tracks[i].Artist, s.Tracks[i].Title, s.Tracks[i].Album = n.Artist, n.Title, n.Album
			s.Tracks[i].AlbumArtist, s.Tracks[i].DurationMs = n.AlbumArtist, n.DurationMs
		}
	}
	return s, err
}
