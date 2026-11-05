package voclean

import (
	"context"
	"math"
	"path/filepath"
	"testing"
)

// extPair is a voice-over in an external file (input 1) against the
// video's original (its stream 0).
func extPair() Pair {
	vo := stream(ExternalIndex("/x/Film.ru.LostFilm.mka"), 6, "rus")
	return Pair{Vo: vo, VoInput: 1, Orig: stream(0, 6, "eng")}
}

// A voice-over downloaded as a file of its own (stage B), 200 ms of its
// start cut off: shifted by its fit's offset it lies on the original, is
// measured eligible at no lag and cleaned like a track of the file.
func TestExternalVoiceOverCleaned(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(testSeconds*sr, 9)
	vo := voiceOver51(orig, 0)
	cut := sr / 5 // 200 ms
	for c := range vo {
		vo[c] = vo[c][cut:]
	}
	video := muxFile(t, ff, "Film.mkv", nil, orig)
	track := muxFile(t, ff, "Film.ru.LostFilm.mka", nil, vo)
	in := Input{Path: video, DurationMs: testSeconds * 1000, External: []ExternalTrack{{Path: track, OffsetMs: 200}}}
	ms, err := Eligible(context.Background(), ff, in, []Pair{extPair()})
	if err != nil {
		t.Fatal(err)
	}
	if !ms[0].Eligible || math.Abs(ms[0].LagMs) > 0.5 {
		t.Fatalf("%+v", ms[0])
	}
	out := filepath.Join(t.TempDir(), "out.mka")
	cleanPCM(t, ff, in, Job{Pair: extPair(), LagMs: ms[0].LagMs, Out: out})
	got := decodeAll(t, ff, out, 0, 6)
	// on the video's timeline from its start: 200 ms of silence first
	if len(got[0]) < len(orig[0])-sr/100 {
		t.Fatalf("length %d, want about %d", len(got[0]), len(orig[0]))
	}
	a, b := int(8.5*sr), int(11.5*sr) // the voice-free centre: the original gone
	if lvl := levelDB(got[2][a:b], vo[2][a-cut:b-cut]); lvl > -30 {
		t.Fatalf("centre still holds the original: %.1f dB", lvl)
	}
}
