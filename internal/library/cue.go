package library

import (
	"bufio"
	"bytes"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

// Cue is a parsed CUE sheet.
type Cue struct {
	Performer, Title, Date, Genre string
	Disc                          int // REM DISCNUMBER, 0 when absent
	Files                         []CueFile
}

// CueFile is one FILE entry with its tracks.
type CueFile struct {
	Name   string
	Tracks []CueTrack
}

// CueTrack is one TRACK; StartMs is its INDEX 01 within the file.
type CueTrack struct {
	Number           int
	Title, Performer string
	StartMs          int64
}

var utf8BOM = []byte("\xEF\xBB\xBF")

// decodeText returns text as UTF-8: UTF-16 with a BOM, UTF-8 (with a BOM
// or valid as is), otherwise Windows-1251.
func decodeText(data []byte) string {
	if bytes.HasPrefix(data, []byte{0xFF, 0xFE}) || bytes.HasPrefix(data, []byte{0xFE, 0xFF}) {
		if out, err := unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder().Bytes(data); err == nil {
			return string(out)
		}
	}
	if bytes.HasPrefix(data, utf8BOM) {
		return string(data[len(utf8BOM):])
	}
	if utf8.Valid(data) {
		return string(data)
	}
	out, err := charmap.Windows1251.NewDecoder().Bytes(data)
	if err != nil {
		return string(data)
	}
	return string(out)
}

// cueValue returns the first argument of a CUE command: a quoted string or
// the next word.
func cueValue(rest string) string {
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, `"`) {
		if end := strings.Index(rest[1:], `"`); end >= 0 {
			return rest[1 : 1+end]
		}
		return strings.TrimSpace(rest[1:])
	}
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		return rest[:i]
	}
	return rest
}

// cueFileName returns the file name of a FILE command ("name" TYPE or an
// unquoted name followed by its type).
func cueFileName(rest string) string {
	rest = strings.TrimSpace(rest)
	if strings.HasPrefix(rest, `"`) {
		return cueValue(rest)
	}
	if i := strings.LastIndexAny(rest, " \t"); i > 0 {
		return strings.TrimSpace(rest[:i])
	}
	return rest
}

// cueTime parses mm:ss:ff (75 frames per second) into milliseconds.
func cueTime(s string) (int64, bool) {
	p := strings.Split(strings.TrimSpace(s), ":")
	if len(p) != 3 {
		return 0, false
	}
	var v [3]int64
	for i, x := range p {
		n, err := strconv.ParseInt(x, 10, 64)
		if err != nil || n < 0 {
			return 0, false
		}
		v[i] = n
	}
	return (v[0]*60+v[1])*1000 + v[2]*1000/75, true
}

// parseCue parses a CUE sheet (UTF-8 or Windows-1251). A sheet without a
// single track is an error.
//
// A track belongs to the FILE of its INDEX 01. EAC's "gaps appended to the
// previous track" puts a track's TRACK line and its INDEX 00 (the pregap)
// in the previous FILE and only its INDEX 01 in its own: the pregap then
// stays with the previous track, so a track-per-file rip is not an image.
// A track without an INDEX 01 starts at its INDEX 00.
func parseCue(data []byte) (Cue, error) {
	var c Cue
	type rawTrack struct {
		CueTrack
		file         int // the FILE it belongs to
		has01, has00 bool
		idx00        int64
		file00       int // the FILE of its INDEX 00
	}
	var tracks []rawTrack
	track := -1 // the track TITLE, PERFORMER and INDEX apply to
	sc := bufio.NewScanner(strings.NewReader(decodeText(data)))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		cmd, rest, _ := strings.Cut(line, " ")
		rest = strings.TrimSpace(rest)
		switch strings.ToUpper(cmd) {
		case "REM":
			key, val, _ := strings.Cut(rest, " ")
			val = cueValue(strings.TrimSpace(val))
			switch strings.ToUpper(key) {
			case "DATE":
				c.Date = val
			case "GENRE":
				if c.Genre == "" {
					c.Genre = val
				}
			case "DISCNUMBER":
				c.Disc, _ = strconv.Atoi(val)
			}
		case "PERFORMER":
			if track >= 0 {
				tracks[track].Performer = cueValue(rest)
			} else {
				c.Performer = cueValue(rest)
			}
		case "TITLE":
			if track >= 0 {
				tracks[track].Title = cueValue(rest)
			} else {
				c.Title = cueValue(rest)
			}
		case "FILE":
			c.Files = append(c.Files, CueFile{Name: cueFileName(rest)})
			// a track still waiting for its INDEX 01 finds it in this file
			if track >= 0 && tracks[track].has01 {
				track = -1
			}
		case "TRACK":
			if len(c.Files) == 0 {
				c.Files = append(c.Files, CueFile{})
			}
			n, _ := strconv.Atoi(cueValue(rest))
			tracks = append(tracks, rawTrack{CueTrack: CueTrack{Number: n}, file: len(c.Files) - 1})
			track = len(tracks) - 1
		case "INDEX":
			if track < 0 {
				continue
			}
			num, at, _ := strings.Cut(rest, " ")
			ms, ok := cueTime(at)
			if !ok {
				continue
			}
			t := &tracks[track]
			switch n, _ := strconv.Atoi(num); n {
			case 1:
				t.StartMs, t.has01, t.file = ms, true, len(c.Files)-1
			case 0:
				if !t.has01 {
					t.idx00, t.has00, t.file00 = ms, true, len(c.Files)-1
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return c, err
	}
	if len(tracks) == 0 {
		return c, errors.New("cue: no tracks")
	}
	for _, t := range tracks {
		if !t.has01 && t.has00 {
			t.StartMs, t.file = t.idx00, t.file00
		}
		c.Files[t.file].Tracks = append(c.Files[t.file].Tracks, t.CueTrack)
	}
	return c, nil
}

// cueAudioFile finds the audio file a CUE FILE entry names, in dir: by its
// name, then by the same stem with any audio extension (a sheet often says
// .wav while the image is .flac). It returns "" when there is none.
func cueAudioFile(di dirIndex, dir, name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if name == "" {
		return ""
	}
	if p := di.find(dir, name); p != "" && musicExt(p) {
		return p
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	var names []string
	for e := range audioExt {
		if e != ".m4b" {
			names = append(names, stem+e)
		}
	}
	sort.Strings(names)
	return di.find(dir, names...)
}
