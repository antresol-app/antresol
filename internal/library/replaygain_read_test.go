package library

import (
	"path/filepath"
	"testing"
)

// TestReadReplayGain: the loudness worker's re-read of an old row's file
// gives what the scanner would (an image: the album gain only).
func TestReadReplayGain(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.flac")
	writeAudio(t, p, map[string]string{"title": "X", "REPLAYGAIN_TRACK_GAIN": "-4.00 dB", "REPLAYGAIN_TRACK_PEAK": "0.8"}, 1, nil)
	rg, err := ReadReplayGain(p, false)
	if err != nil || !sameRG(rg, ReplayGain{TrackGain: fp(-4), TrackPeak: fp(0.8)}) {
		t.Fatalf("plain: %s %v", rgString(rg), err)
	}
	rg, err = ReadReplayGain(p, true)
	if err != nil || !sameRG(rg, ReplayGain{AlbumGain: fp(-4), AlbumPeak: fp(0.8)}) {
		t.Fatalf("image: %s %v", rgString(rg), err)
	}
}
