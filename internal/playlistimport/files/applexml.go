package files

import (
	"bytes"
	"encoding/xml"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/antresol-app/antresol/internal/playlistimport"
)

// An Apple Music (iTunes) «Library.xml» is an XML property list. It is
// read with encoding/xml, which never expands DTD entities (internal or
// external; an entity it does not know is a syntax error in strict mode)
// and fetches nothing, so a billion laughs or an XXE cannot work. Nesting
// and the number of values are bounded too.
const (
	plistMaxDepth  = 16
	plistMaxValues = 20_000_000
)

var errPlist = errors.New("plist: malformed")

type plistReader struct {
	d      *xml.Decoder
	values int
}

// parseAppleXML reads the music tracks of «Tracks» (loved or favorited:
// liked) and the user's playlists of «Playlists»; «Любимые песни» /
// «Favorite Songs» makes its songs liked.
func parseAppleXML(data []byte) (playlistimport.Snapshot, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = true
	p := &plistReader{d: d}
	b := newBuilder(playlistimport.ServiceApple)
	if err := p.root(b); err != nil {
		if errors.Is(err, playlistimport.ErrTooLarge) {
			return playlistimport.Snapshot{}, err
		}
		return playlistimport.Snapshot{}, ErrUnreadable
	}
	return b.snap, nil
}

// next returns the next start or end element, skipping text, comments and
// directives (the DOCTYPE).
func (p *plistReader) next() (xml.Token, error) {
	for {
		tok, err := p.d.Token()
		if err != nil {
			return nil, err
		}
		switch tok.(type) {
		case xml.StartElement, xml.EndElement:
			if p.values++; p.values > plistMaxValues {
				return nil, playlistimport.ErrTooLarge
			}
			return tok, nil
		}
	}
}

// root walks <plist><dict>: its «Tracks» dict and «Playlists» array are
// read one entry at a time; the rest is skipped.
func (p *plistReader) root(b *builder) error {
	tok, err := p.next()
	if err != nil {
		return err
	}
	if s, ok := tok.(xml.StartElement); !ok || s.Name.Local != "plist" {
		return errPlist
	}
	if tok, err = p.next(); err != nil {
		return err
	}
	if s, ok := tok.(xml.StartElement); !ok || s.Name.Local != "dict" {
		return errPlist
	}
	byID := map[string]string{} // Track ID → song key (music only)
	var favorites []string      // keys of «Любимые песни»
	err = p.dictKeys(func(key string, start xml.StartElement) error {
		switch {
		case key == "Tracks" && start.Name.Local == "dict":
			return p.dictKeys(func(id string, start xml.StartElement) error {
				v, err := p.value(start, 2)
				if err != nil {
					return err
				}
				if m, ok := v.(map[string]any); ok {
					return addAppleTrack(b, byID, id, m)
				}
				return nil
			})
		case key == "Playlists" && start.Name.Local == "array":
			return p.arrayItems(func(start xml.StartElement) error {
				v, err := p.value(start, 2)
				if err != nil {
					return err
				}
				if m, ok := v.(map[string]any); ok {
					fav, err := addApplePlaylist(b, byID, m)
					favorites = append(favorites, fav...)
					return err
				}
				return nil
			})
		}
		_, err := p.value(start, 1)
		return err
	})
	if err != nil {
		return err
	}
	for _, k := range favorites {
		b.snap.Tracks[b.at[k]].Liked = true
	}
	return nil
}

// dictKeys calls f with each <key> of the open <dict> and the start of
// its value, which f must consume; it returns at </dict>.
func (p *plistReader) dictKeys(f func(key string, value xml.StartElement) error) error {
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			if t.Name.Local != "key" {
				return errPlist
			}
			key, err := p.text()
			if err != nil {
				return err
			}
			tok, err := p.next()
			if err != nil {
				return err
			}
			start, ok := tok.(xml.StartElement)
			if !ok {
				return errPlist
			}
			if err := f(key, start); err != nil {
				return err
			}
		}
	}
}

// arrayItems calls f with the start of each value of the open <array>.
func (p *plistReader) arrayItems(f func(value xml.StartElement) error) error {
	for {
		tok, err := p.next()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.EndElement:
			return nil
		case xml.StartElement:
			if err := f(t); err != nil {
				return err
			}
		}
	}
}

// text reads the character data of the open element up to its end.
func (p *plistReader) text() (string, error) {
	var sb strings.Builder
	for {
		tok, err := p.d.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			sb.Write(t)
		case xml.EndElement:
			return sb.String(), nil
		case xml.StartElement:
			return "", errPlist
		}
	}
}

// value reads one plist value whose start was read: dict → map, array →
// slice, string, integer → int64, true/false → bool, date → time.Time;
// data and real are read as their text. depth bounds nesting.
func (p *plistReader) value(start xml.StartElement, depth int) (any, error) {
	if depth > plistMaxDepth {
		return nil, errPlist
	}
	switch start.Name.Local {
	case "dict":
		m := map[string]any{}
		err := p.dictKeys(func(key string, v xml.StartElement) error {
			val, err := p.value(v, depth+1)
			m[key] = val
			return err
		})
		return m, err
	case "array":
		var a []any
		err := p.arrayItems(func(v xml.StartElement) error {
			val, err := p.value(v, depth+1)
			a = append(a, val)
			return err
		})
		return a, err
	case "true", "false":
		if _, err := p.text(); err != nil {
			return nil, err
		}
		return start.Name.Local == "true", nil
	case "integer":
		s, err := p.text()
		if err != nil {
			return nil, err
		}
		n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		return n, nil
	case "date":
		s, err := p.text()
		if err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(s)); err == nil {
			return t.UTC(), nil
		}
		return nil, nil
	case "string", "data", "real", "key":
		return p.text()
	}
	return nil, errPlist
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return strings.TrimSpace(s)
}

func flag(m map[string]any, k string) bool {
	v, _ := m[k].(bool)
	return v
}

func integer(m map[string]any, k string) int64 {
	n, _ := m[k].(int64)
	return n
}

// trackID is a «Track ID» as the Tracks dict keys it.
func trackID(m map[string]any) string {
	if n, ok := m["Track ID"].(int64); ok {
		return strconv.FormatInt(n, 10)
	}
	return ""
}

// addAppleTrack adds a music track of «Tracks» (id: its dict key);
// podcasts, films, TV, music videos, audiobooks and anything with video
// are not music.
func addAppleTrack(b *builder, byID map[string]string, id string, m map[string]any) error {
	kind := strings.ToLower(str(m, "Kind"))
	if flag(m, "Podcast") || flag(m, "Movie") || flag(m, "TV Show") || flag(m, "Music Video") || flag(m, "Has Video") ||
		strings.Contains(kind, "аудиокниг") || strings.Contains(kind, "audiobook") {
		return nil
	}
	if n := trackID(m); n != "" {
		id = n
	}
	key := str(m, "Persistent ID")
	if key == "" {
		key = strings.TrimSpace(id)
	}
	t := playlistimport.SnapTrack{Key: key, Artist: str(m, "Artist"), Title: str(m, "Name"), Album: str(m, "Album"),
		AlbumArtist: str(m, "Album Artist"), DurationMs: max(integer(m, "Total Time"), 0),
		Liked: flag(m, "Loved") || flag(m, "Favorited")}
	if t.Artist == "" {
		t.Artist = t.AlbumArtist
	}
	if d, ok := m["Date Added"].(time.Time); ok {
		t.AddedAt = &d
	}
	if t.Artist == "" || t.Title == "" {
		return nil
	}
	k, err := b.add(t)
	if err == nil {
		byID[strings.TrimSpace(id)] = k
	}
	return err
}

// isFavorites: the playlist of loved songs, by its name.
func isFavorites(name string) bool {
	n := norm(name)
	return n == "любимые песни" || n == "favorite songs"
}

// addApplePlaylist adds a user's playlist in its order; the library,
// built-in, folder and smart playlists are skipped, except «Любимые
// песни», whose song keys it returns (they become liked).
func addApplePlaylist(b *builder, byID map[string]string, m map[string]any) (favorites []string, err error) {
	name := str(m, "Name")
	var keys []string
	items, _ := m["Playlist Items"].([]any)
	for _, it := range items {
		if im, ok := it.(map[string]any); ok {
			if k, ok := byID[trackID(im)]; ok {
				keys = append(keys, k)
			}
		}
	}
	if isFavorites(name) {
		return keys, nil
	}
	_, distinguished := m["Distinguished Kind"]
	_, smart := m["Smart Info"]
	if name == "" || flag(m, "Master") || distinguished || flag(m, "Folder") || smart {
		return nil, nil
	}
	p, err := b.playlist(str(m, "Playlist Persistent ID"), name)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if err := b.entry(p, k); err != nil {
			return nil, err
		}
	}
	return nil, nil
}
