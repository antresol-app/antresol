// Package files reads a music library export a user uploads («Файл»):
// CSV (the universal format, Exportify, TuneMyMusic), M3U/M3U8 playlists,
// an Apple Music Library.xml and a Google Takeout zip. Uploads are
// untrusted: every parser bounds what it reads and nothing in a file is
// ever executed, fetched or expanded.
package files

import (
	"bytes"
	"errors"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/antresol-app/antresol/internal/playlistimport"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

// Errors (their text is shown as is).
var (
	ErrUnreadable = errors.New("Не удалось прочитать файл")
	ErrNoColumns  = errors.New("Не удалось прочитать CSV: нет колонок «Исполнитель» и «Название»")
)

// MaxUpload bounds one upload (all its files together).
const MaxUpload = 64 << 20

// Parse reads one uploaded file, by its extension and its first bytes:
// a zip is a Google Takeout, an XML plist a Library.xml, an M3U/M3U8 a
// playlist, a CSV a table of songs. ErrUnreadable: none of them;
// ErrNoColumns: a CSV without artist and title; playlistimport.ErrTooLarge:
// beyond the snapshot limits.
func Parse(name string, data []byte) (playlistimport.Snapshot, error) {
	base := path.Base(strings.ReplaceAll(name, `\`, "/"))
	ext := strings.ToLower(path.Ext(base))
	stem := strings.TrimSpace(strings.TrimSuffix(base, path.Ext(base)))
	var (
		s   playlistimport.Snapshot
		err error
	)
	switch {
	case len(bytes.TrimSpace(data)) == 0:
		return s, ErrUnreadable
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		s, err = parseTakeout(data)
	case ext == ".zip":
		return s, ErrUnreadable
	case isPlist(data):
		s, err = parseAppleXML(data)
	case ext == ".xml":
		return s, ErrUnreadable
	default:
		text, ok := decodeText(data)
		switch {
		case !ok:
			return s, ErrUnreadable
		case ext == ".m3u" || ext == ".m3u8" || bytes.HasPrefix(bytes.TrimSpace(text), []byte("#EXTM3U")):
			s, err = parseM3U(stem, string(text))
		case ext == ".csv" || ext == ".tsv" || ext == ".txt":
			s, err = parseCSV(stem, text)
		default:
			return s, ErrUnreadable
		}
	}
	if err != nil {
		return playlistimport.Snapshot{}, err
	}
	if len(s.Tracks) == 0 {
		return playlistimport.Snapshot{}, ErrUnreadable
	}
	return s, nil
}

// isPlist: an XML document whose root is <plist> (within its first 4 KiB,
// after the declaration and the DOCTYPE).
func isPlist(data []byte) bool {
	head := data[:min(len(data), 4096)]
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	head = bytes.TrimSpace(head)
	return bytes.HasPrefix(head, []byte("<")) && bytes.Contains(head, []byte("<plist"))
}

// decodeText turns an uploaded text file into UTF-8 (UTF-8 text is
// returned as is, not copied): a BOM (UTF-8 or UTF-16) is honoured; text
// that is not UTF-8 is read as Windows-1251 (what Excel and old players
// save in Russia). False: binary (NUL bytes).
func decodeText(data []byte) ([]byte, bool) {
	var text []byte
	switch {
	case bytes.HasPrefix(data, []byte("\xef\xbb\xbf")) && utf8.Valid(data[3:]):
		text = data[3:]
	case isUTF16(data):
		b, err := unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder().Bytes(data)
		if err != nil {
			return nil, false
		}
		text = b
	case utf8.Valid(data):
		text = data
	default:
		b, err := charmap.Windows1251.NewDecoder().Bytes(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
		if err != nil {
			return nil, false
		}
		text = b
	}
	text = bytes.TrimPrefix(text, []byte("\xef\xbb\xbf"))
	return text, bytes.IndexByte(text, 0) < 0
}

// isUTF16: a UTF-16 BOM, and NUL bytes soon after it (every ASCII
// character, a line end or a comma, has one); a Windows-1251 text that
// starts with «яю» has none.
func isUTF16(data []byte) bool {
	if !bytes.HasPrefix(data, []byte("\xff\xfe")) && !bytes.HasPrefix(data, []byte("\xfe\xff")) {
		return false
	}
	return len(data)%2 == 0 && bytes.IndexByte(data[2:min(len(data), 4096)], 0) >= 0
}

// Merger makes one snapshot of several uploaded files, file by file: of
// their service when all agree (one Library.xml: apple; one Takeout:
// youtube), else of service file. A song in several files is one song
// (liked if any copy is, its fields filled in from every copy), so the
// snapshot limits count unique songs; playlists of one id (or, without
// one, one name) are joined in file order.
type Merger struct {
	b     *builder
	files int
}

// NewMerger starts an empty merge.
func NewMerger() *Merger { return &Merger{b: newBuilder(playlistimport.ServiceFile)} }

// Add merges one file's snapshot (playlistimport.ErrTooLarge: past the
// snapshot limits).
func (m *Merger) Add(s playlistimport.Snapshot) error {
	if m.files++; m.files == 1 {
		m.b.snap.Service = s.Service
	} else if s.Service != m.b.snap.Service {
		m.b.snap.Service = playlistimport.ServiceFile
	}
	for _, t := range s.Tracks {
		if _, err := m.b.add(t); err != nil {
			return err
		}
	}
	for _, sp := range s.Playlists {
		p, err := m.b.playlist(sp.ID, sp.Name)
		if err != nil {
			return err
		}
		for _, k := range sp.Entries {
			if err := m.b.entry(p, k); err != nil {
				return err
			}
		}
	}
	if len(m.b.snap.Albums)+len(s.Albums) > playlistimport.MaxTracks {
		return playlistimport.ErrTooLarge
	}
	m.b.snap.Albums = append(m.b.snap.Albums, s.Albums...)
	return nil
}

// Snapshot is the merged snapshot.
func (m *Merger) Snapshot() playlistimport.Snapshot { return m.b.snap }

// Merge merges several files' snapshots (see Merger).
func Merge(snaps []playlistimport.Snapshot) (playlistimport.Snapshot, error) {
	m := NewMerger()
	for _, s := range snaps {
		if err := m.Add(s); err != nil {
			return playlistimport.Snapshot{}, err
		}
	}
	return m.Snapshot(), nil
}

// Unresolved counts the songs a file named only by an id (a Takeout
// playlist video not in the library; the parsers keep no other song
// without a title, and a merge names one another file names): Normalize
// drops them unless a connector filled their names in first.
func Unresolved(s playlistimport.Snapshot) int {
	n := 0
	for _, t := range s.Tracks {
		if strings.TrimSpace(t.Title) == "" {
			n++
		}
	}
	return n
}

// norm is the comparison form of a name: lower case, single spaces.
func norm(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// songKey identifies a song a file gives no id for, by artist and title:
// one song in two files (one naming its album, one not) is one song.
func songKey(artist, title string) string {
	return norm(artist) + "|" + norm(title)
}

// maxEntries bounds the playlist entries of one file.
const maxEntries = 4 * playlistimport.MaxTracks

// builder collects a file's songs and playlists in order.
type builder struct {
	snap    playlistimport.Snapshot
	entries int
	at      map[string]int // track key → index
	pl      map[string]int // playlist name (norm) → index
}

func newBuilder(service string) *builder {
	return &builder{snap: playlistimport.Snapshot{Service: service}, at: map[string]int{}, pl: map[string]int{}}
}

// add records a song (a song already seen gets liked, a date and missing
// fields) and returns its key; ErrTooLarge past the snapshot limit.
func (b *builder) add(t playlistimport.SnapTrack) (string, error) {
	t.Artist, t.Title, t.Album = strings.TrimSpace(t.Artist), strings.TrimSpace(t.Title), strings.TrimSpace(t.Album)
	if t.Key == "" {
		t.Key = songKey(t.Artist, t.Title)
	}
	if i, ok := b.at[t.Key]; ok {
		d := &b.snap.Tracks[i]
		d.Liked = d.Liked || t.Liked
		if d.AddedAt == nil || (t.AddedAt != nil && t.AddedAt.Before(*d.AddedAt)) {
			d.AddedAt = t.AddedAt
		}
		for _, f := range [][2]*string{{&d.Artist, &t.Artist}, {&d.Title, &t.Title}, {&d.Album, &t.Album},
			{&d.AlbumArtist, &t.AlbumArtist}, {&d.ISRC, &t.ISRC}} {
			if *f[0] == "" {
				*f[0] = *f[1]
			}
		}
		if d.DurationMs == 0 {
			d.DurationMs = t.DurationMs
		}
		return t.Key, nil
	}
	if len(b.snap.Tracks) >= playlistimport.MaxTracks {
		return "", playlistimport.ErrTooLarge
	}
	b.at[t.Key] = len(b.snap.Tracks)
	b.snap.Tracks = append(b.snap.Tracks, t)
	return t.Key, nil
}

// playlist returns the playlist of a name, made on first use.
func (b *builder) playlist(id, name string) (*playlistimport.SnapPlaylist, error) {
	k := norm(name)
	if id != "" {
		k = "id:" + id
	}
	if i, ok := b.pl[k]; ok {
		return &b.snap.Playlists[i], nil
	}
	if len(b.snap.Playlists) >= playlistimport.MaxPlaylists {
		return nil, playlistimport.ErrTooLarge
	}
	b.pl[k] = len(b.snap.Playlists)
	b.snap.Playlists = append(b.snap.Playlists, playlistimport.SnapPlaylist{ID: id, Name: name, Readable: true, Entries: []string{}})
	return &b.snap.Playlists[len(b.snap.Playlists)-1], nil
}

// entry appends a song to a playlist (ErrTooLarge past maxEntries).
func (b *builder) entry(p *playlistimport.SnapPlaylist, key string) error {
	if b.entries++; b.entries > maxEntries {
		return playlistimport.ErrTooLarge
	}
	p.Entries = append(p.Entries, key)
	return nil
}
