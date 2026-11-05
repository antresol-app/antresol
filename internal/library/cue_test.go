package library

import (
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

func TestParseCueFrames(t *testing.T) {
	c, err := parseCue([]byte("FILE \"a.flac\" WAVE\n  TRACK 01 AUDIO\n    INDEX 01 00:00:00\n  TRACK 02 AUDIO\n    INDEX 00 01:00:00\n    INDEX 01 01:02:37\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Files) != 1 || len(c.Files[0].Tracks) != 2 {
		t.Fatalf("%+v", c)
	}
	if got := c.Files[0].Tracks[1].StartMs; got != 62493 {
		t.Fatalf("start %d, want 62493", got)
	}
}

// EAC "gaps appended to the previous track": a track's TRACK line and its
// INDEX 00 (the pregap) sit in the previous FILE, its INDEX 01 in its own.
// The track belongs to the file of its INDEX 01; the pregap stays with the
// previous track (Metallica (1991) showed a 1.5 s "Sad But True" that failed
// to play, and "Enter Sandman" cut short).
const cueGapsAppended = "FILE \"01. Enter Sandman.flac\" WAVE\n  TRACK 01 AUDIO\n    TITLE \"Enter Sandman\"\n    INDEX 01 00:00:00\n" +
	"  TRACK 02 AUDIO\n    TITLE \"Sad But True\"\n    INDEX 00 05:30:29\n" +
	"FILE \"02. Sad But True.flac\" WAVE\n    INDEX 01 00:00:00\n" +
	"  TRACK 03 AUDIO\n    TITLE \"Holier Than Thou\"\n    INDEX 00 05:24:03\n" +
	"FILE \"03. Holier Than Thou.flac\" WAVE\n    INDEX 01 00:00:00\n"

func TestParseCueGapsAppended(t *testing.T) {
	c, err := parseCue([]byte(cueGapsAppended))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Files) != 3 {
		t.Fatalf("files %+v", c.Files)
	}
	for i, want := range []string{"Enter Sandman", "Sad But True", "Holier Than Thou"} {
		f := c.Files[i]
		if len(f.Tracks) != 1 || f.Tracks[0].Title != want || f.Tracks[0].StartMs != 0 || f.Tracks[0].Number != i+1 {
			t.Fatalf("file %d %q: %+v", i, f.Name, f.Tracks)
		}
	}
}

const cueText = "REM GENRE Rock\r\nREM DATE 1988\r\nPERFORMER \"Кино\"\r\nTITLE \"Группа крови\"\r\nFILE \"Кино - Группа крови.wav\" WAVE\r\n" +
	"  TRACK 01 AUDIO\r\n    TITLE \"Группа крови\"\r\n    PERFORMER \"Кино\"\r\n    INDEX 01 00:00:00\r\n" +
	"  TRACK 02 AUDIO\r\n    TITLE \"Закрой за мной дверь, я ухожу\"\r\n    INDEX 01 00:02:00\r\n" +
	"  TRACK 03 AUDIO\r\n    TITLE \"Война\"\r\n    INDEX 01 00:04:00\r\n"

func TestParseCueEncodings(t *testing.T) {
	cp, err := charmap.Windows1251.NewEncoder().String(cueText)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"utf8":    []byte(cueText),
		"utf8bom": append([]byte("\xEF\xBB\xBF"), cueText...),
		"cp1251":  []byte(cp),
		"utf16le": utf16(t, cueText),
	} {
		c, err := parseCue(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c.Performer != "Кино" || c.Title != "Группа крови" || c.Date != "1988" || c.Genre != "Rock" {
			t.Fatalf("%s: header %+v", name, c)
		}
		if len(c.Files) != 1 || c.Files[0].Name != "Кино - Группа крови.wav" || len(c.Files[0].Tracks) != 3 {
			t.Fatalf("%s: files %+v", name, c.Files)
		}
		tr := c.Files[0].Tracks
		if tr[1].Title != "Закрой за мной дверь, я ухожу" || tr[1].Number != 2 || tr[1].StartMs != 2000 || tr[0].Performer != "Кино" || tr[2].StartMs != 4000 {
			t.Fatalf("%s: tracks %+v", name, tr)
		}
	}
}

func TestParseCueBroken(t *testing.T) {
	if _, err := parseCue([]byte("garbage\x00\x01 not a cue")); err == nil {
		t.Fatal("want error for a cue without tracks")
	}
}

func utf16(t *testing.T, s string) []byte {
	t.Helper()
	b, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
