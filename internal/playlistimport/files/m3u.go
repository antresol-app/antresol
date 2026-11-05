package files

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/antresol-app/antresol/internal/playlistimport"
)

// trackNo is a leading track number of a file name: «01 - », «1. »,
// «03_», or a zero-padded «01 ». A bare «21 » is kept: «21 Guns».
var trackNo = regexp.MustCompile(`^(\d{1,3}\s*[-._]\s*|0\d{1,2}\s+)`)

// parseM3U reads an M3U/M3U8 playlist: «#EXTINF:<sec>,<Artist> - <Title>»
// names a song; a path with no #EXTINF names it by its folders
// («Artist/Album/NN Title.ext») or its file name («Artist - Title.ext»).
// #PLAYLIST: or the file name names the playlist. Paths are never opened.
func parseM3U(stem, text string) (playlistimport.Snapshot, error) {
	b := newBuilder(playlistimport.ServiceFile)
	name := strings.TrimSpace(stem)
	var songs []playlistimport.SnapTrack
	var pending *playlistimport.SnapTrack // the #EXTINF waiting for its path
	flush := func() {
		if pending != nil {
			songs = append(songs, *pending)
			pending = nil
		}
	}
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#PLAYLIST:"):
			if n := strings.TrimSpace(strings.TrimPrefix(line, "#PLAYLIST:")); n != "" {
				name = n
			}
		case strings.HasPrefix(line, "#EXTINF:"):
			flush()
			t := extinf(strings.TrimPrefix(line, "#EXTINF:"))
			pending = &t
		case strings.HasPrefix(line, "#"):
		default:
			if pending != nil {
				if pending.Artist == "" || pending.Title == "" {
					p := fromPath(line)
					if p.Artist != "" && p.Title != "" {
						pending.Artist, pending.Title, pending.Album = p.Artist, p.Title, p.Album
					}
				}
				flush()
			} else if t := fromPath(line); t.Title != "" {
				songs = append(songs, t)
			}
		}
		if len(songs) > maxEntries {
			return playlistimport.Snapshot{}, playlistimport.ErrTooLarge
		}
	}
	flush()
	if name == "" {
		name = "M3U"
	}
	p, err := b.playlist("", name)
	if err != nil {
		return playlistimport.Snapshot{}, err
	}
	for _, t := range songs {
		if t.Artist == "" || t.Title == "" {
			continue
		}
		key, err := b.add(t)
		if err != nil {
			return playlistimport.Snapshot{}, err
		}
		if err := b.entry(p, key); err != nil {
			return playlistimport.Snapshot{}, err
		}
	}
	return b.snap, nil
}

// extinf reads «<sec>[ attrs],<Artist> - <Title>».
func extinf(s string) playlistimport.SnapTrack {
	head, rest, _ := strings.Cut(s, ",")
	var t playlistimport.SnapTrack
	if f := strings.Fields(head); len(f) > 0 {
		if sec, err := strconv.ParseFloat(f[0], 64); err == nil && sec > 0 && sec < 1e7 {
			t.DurationMs = int64(sec * 1000)
		}
	}
	if a, title, ok := strings.Cut(rest, " - "); ok {
		t.Artist, t.Title = strings.TrimSpace(a), strings.TrimSpace(title)
	} else {
		t.Title = strings.TrimSpace(rest)
	}
	return t
}

// fromPath names a song by its path: a file name «Artist - Title.ext»
// first (its folders may be anything: «Music/Unsorted/»), else
// «…/Artist/Album/NN Title.ext». A URL names nothing.
func fromPath(p string) playlistimport.SnapTrack {
	if strings.Contains(p, "://") {
		return playlistimport.SnapTrack{}
	}
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) == 0 {
		return playlistimport.SnapTrack{}
	}
	file := parts[len(parts)-1]
	if i := strings.LastIndexByte(file, '.'); i > 0 {
		file = file[:i]
	}
	file = strings.TrimSpace(trackNo.ReplaceAllString(file, ""))
	if a, title, ok := strings.Cut(file, " - "); ok {
		return playlistimport.SnapTrack{Artist: strings.TrimSpace(a), Title: strings.TrimSpace(title)}
	}
	if len(parts) >= 3 && !strings.HasSuffix(parts[len(parts)-3], ":") {
		return playlistimport.SnapTrack{Artist: parts[len(parts)-3], Album: parts[len(parts)-2], Title: file}
	}
	return playlistimport.SnapTrack{}
}
