package library

import (
	"context"
	"math"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/dhowden/tag"
)

func fp(v float64) *float64 { return &v }

func sameFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return math.Abs(*a-*b) < 1e-9
}

func sameRG(a, b ReplayGain) bool {
	return sameFloat(a.TrackGain, b.TrackGain) && sameFloat(a.TrackPeak, b.TrackPeak) &&
		sameFloat(a.AlbumGain, b.AlbumGain) && sameFloat(a.AlbumPeak, b.AlbumPeak)
}

func TestReplayGainTags(t *testing.T) {
	for _, c := range []struct {
		name string
		raw  map[string]any
		want ReplayGain
	}{
		{"vorbis (FLAC, Ogg)", map[string]any{"replaygain_track_gain": "-6.48 dB", "replaygain_track_peak": "0.988525",
			"replaygain_album_gain": "-7.10 dB", "replaygain_album_peak": "1.000000"},
			ReplayGain{fp(-6.48), fp(0.988525), fp(-7.10), fp(1)}},
		{"upper case, plus sign, decimal comma", map[string]any{"REPLAYGAIN_TRACK_GAIN": "+2,10 dB", "REPLAYGAIN_TRACK_PEAK": "0,5"},
			ReplayGain{TrackGain: fp(2.10), TrackPeak: fp(0.5)}},
		{"ID3v2 TXXX", map[string]any{
			"TXXX":   &tag.Comm{Description: "REPLAYGAIN_TRACK_GAIN", Text: "-3.00 dB"},
			"TXXX_0": &tag.Comm{Description: "replaygain_track_peak", Text: "0.75"},
			"TXXX_1": &tag.Comm{Description: "MusicBrainz Album Id", Text: "x"},
		}, ReplayGain{TrackGain: fp(-3), TrackPeak: fp(0.75)}},
		{"MP4 freeform (data with NULs)", map[string]any{"replaygain_album_gain": "\x00\x00\x00\x00-1.5 dB"},
			ReplayGain{AlbumGain: fp(-1.5)}},
		{"Opus R128 (Q7.8 on -23 LUFS)", map[string]any{"r128_track_gain": "-1280", "r128_album_gain": "256"},
			ReplayGain{TrackGain: fp(-5 + 5), AlbumGain: fp(1 + 5)}},
		{"ReplayGain wins over R128", map[string]any{"r128_track_gain": "0", "replaygain_track_gain": "-2 dB"},
			ReplayGain{TrackGain: fp(-2)}},
		{"junk ignored", map[string]any{"replaygain_track_gain": "loud", "replaygain_track_peak": "0",
			"replaygain_album_gain": "-99 dB", "replaygain_album_peak": "-1", "r128_album_gain": "99999"},
			ReplayGain{}},
		{"none", map[string]any{"title": "x", "year": 1999}, ReplayGain{}},
	} {
		if got := replayGainOf(c.raw); !sameRG(got, c.want) {
			t.Errorf("%s: got %s, want %s", c.name, rgString(got), rgString(c.want))
		}
	}
}

func rgString(rg ReplayGain) string {
	f := func(v *float64) string {
		if v == nil {
			return "nil"
		}
		return strconv.FormatFloat(*v, 'g', -1, 64)
	}
	return "{" + f(rg.TrackGain) + " " + f(rg.TrackPeak) + " " + f(rg.AlbumGain) + " " + f(rg.AlbumPeak) + "}"
}

// TestScanReadsReplayGain: the scanner reads the tags of real files (FLAC
// Vorbis comments, MP3 TXXX) and the track lists give them, a CUE image's
// tags as its album gain only.
func TestScanReadsReplayGain(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "A", "Album")
	writeAudio(t, filepath.Join(dir, "01.flac"), map[string]string{"artist": "A", "album": "Album", "track": "1", "title": "One",
		"REPLAYGAIN_TRACK_GAIN": "-6.50 dB", "REPLAYGAIN_TRACK_PEAK": "0.9", "REPLAYGAIN_ALBUM_GAIN": "-7.00 dB", "REPLAYGAIN_ALBUM_PEAK": "0.95"}, 1, nil)
	writeAudio(t, filepath.Join(dir, "02.mp3"), map[string]string{"artist": "A", "album": "Album", "track": "2", "title": "Two",
		"REPLAYGAIN_TRACK_GAIN": "-3.25 dB", "REPLAYGAIN_TRACK_PEAK": "0.5"}, 1, nil)
	writeAudio(t, filepath.Join(dir, "03.flac"), map[string]string{"artist": "A", "album": "Album", "track": "3", "title": "Three"}, 1, nil)
	scanMusicOK(t, s)
	_, tracks := oneAlbum(t, s.Repo)
	if len(tracks) != 3 {
		t.Fatalf("tracks %d", len(tracks))
	}
	want := []Loudness{
		{TrackGain: fp(-6.5), TrackPeak: fp(0.9), AlbumGain: fp(-7), AlbumPeak: fp(0.95)},
		{TrackGain: fp(-3.25), TrackPeak: fp(0.5)},
		{},
	}
	for i, tr := range tracks {
		g := tr.Gain
		if !sameRG(ReplayGain(g), ReplayGain(want[i])) {
			t.Errorf("track %d gain %s, want %s", i+1, rgString(ReplayGain(g)), rgString(ReplayGain(want[i])))
		}
	}
	var scanned, pending int
	if err := s.Repo.db.QueryRowContext(context.Background(), `SELECT SUM(rg_scanned), SUM(loud_state=0) FROM tracks`).Scan(&scanned, &pending); err != nil {
		t.Fatal(err)
	}
	if scanned != 3 || pending != 3 {
		t.Fatalf("rg_scanned %d, loud_state 0: %d", scanned, pending)
	}
}

func TestImageReplayGainIsTheAlbums(t *testing.T) {
	e := &musicEntry{image: true, tags: audioTags{replayGain: ReplayGain{TrackGain: fp(-4), TrackPeak: fp(0.8)}}}
	if g := trackReplayGain(e); g.TrackGain != nil || !sameFloat(g.AlbumGain, fp(-4)) || !sameFloat(g.AlbumPeak, fp(0.8)) {
		t.Fatalf("image without album tags: %s", rgString(g))
	}
	e.tags.replayGain.AlbumGain = fp(-5)
	if g := trackReplayGain(e); g.TrackGain != nil || !sameFloat(g.AlbumGain, fp(-5)) || g.AlbumPeak != nil {
		t.Fatalf("image with album tags: %s", rgString(g))
	}
	e.image = false
	if g := trackReplayGain(e); !sameFloat(g.TrackGain, fp(-4)) || !sameFloat(g.AlbumGain, fp(-5)) {
		t.Fatalf("plain track: %s", rgString(g))
	}
}

// TestMeasuredGainInTracks: a measured loudness gives the track gain (on
// -18 LUFS) where no tag does; the album values follow loud_album_*.
func TestMeasuredGainInTracks(t *testing.T) {
	s, root := musicScanner(t)
	dir := filepath.Join(root, "B", "Bee")
	writeAudio(t, filepath.Join(dir, "01.flac"), map[string]string{"artist": "B", "album": "Bee", "track": "1", "title": "One"}, 1, nil)
	scanMusicOK(t, s)
	_, tracks := oneAlbum(t, s.Repo)
	if _, err := s.Repo.db.Exec(`UPDATE tracks SET loud_state=1, loud_lufs=-10, loud_peak=0.7, loud_album_gain=-8.5, loud_album_peak=0.7`); err != nil {
		t.Fatal(err)
	}
	tr, err := s.Repo.TrackByID(context.Background(), tracks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Loudness{fp(-8), fp(0.7), fp(-8.5), fp(0.7)}); !sameRG(ReplayGain(tr.Gain), ReplayGain(want)) {
		t.Fatalf("gain %s", rgString(ReplayGain(tr.Gain)))
	}
	// the file rewritten: its measurement is stale
	if _, err := s.Repo.db.Exec(`UPDATE files SET mtime=mtime+1`); err != nil {
		t.Fatal(err)
	}
	var state int
	var lufs *float64
	if err := s.Repo.db.QueryRow(`SELECT loud_state, loud_lufs FROM tracks`).Scan(&state, &lufs); err != nil {
		t.Fatal(err)
	}
	if state != 0 || lufs != nil {
		t.Fatalf("after a file change: state %d lufs %v", state, lufs)
	}
}
