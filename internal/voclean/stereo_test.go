package voclean

import (
	"math"
	"path/filepath"
	"testing"
)

// voiceOverStereo: a stereo voice-over of a 5.1 original — the original's
// stereo downmix ducked (gain sliding 0.3 → 0.55) with the voice in the
// middle of both channels. dub: the original's dialogue left out (the M&E
// bed only), as a dub has it.
func voiceOverStereo(orig [][]float32, delay int, dub bool) [][]float32 {
	n := len(orig[0])
	vo := [][]float32{make([]float32, n), make([]float32, n)}
	f := make([]float32, len(orig))
	for i := range n {
		j := i - delay
		for c := range f {
			f[c] = 0
			if j >= 0 && j < n {
				f[c] = orig[c][j]
			}
		}
		if dub && j >= 0 && j < n {
			f[2] -= float32(english(j))
		}
		l, r := downmixLR(f)
		g := 0.3 + 0.25*float64(i)/float64(n)
		vo[0][i], vo[1][i] = float32(g*l), float32(g*r)
		if voiceOn(i) {
			v := float32(0.7071 * russian(i))
			vo[0][i] += v
			vo[1][i] += v
		}
	}
	return vo
}

func TestEligibleStereoOver51(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(testSeconds*sr, 31)
	path := muxTracks(t, ff, voiceOverStereo(orig, 0, false), orig)
	m := eligible1(t, ff, path, pair(2, 6), testSeconds*1000)
	if !m.Eligible || math.Abs(m.LagMs) > 0.5 {
		t.Fatalf("a stereo voice-over over a 5.1 original: %+v", m)
	}
	// delayed 30 ms
	path = muxTracks(t, ff, voiceOverStereo(orig, 30*sr/1000, false), orig)
	if m := eligible1(t, ff, path, pair(2, 6), testSeconds*1000); !m.Eligible || math.Abs(m.LagMs-30) > 0.5 {
		t.Fatalf("delayed 30 ms: %+v", m)
	}
}

func TestEligibleStereoDubRejected(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(testSeconds*sr, 32)
	path := muxTracks(t, ff, voiceOverStereo(orig, 0, true), orig)
	if m := eligible1(t, ff, path, pair(2, 6), testSeconds*1000); m.Eligible {
		t.Fatalf("a stereo dub (no English under it) taken for a voice-over: %+v", m)
	}
}

func TestCleanStereoOver51(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(testSeconds*sr, 33)
	vo := voiceOverStereo(orig, 0, false)
	path := muxTracks(t, ff, vo, orig)
	m := eligible1(t, ff, path, pair(2, 6), testSeconds*1000)
	out := filepath.Join(t.TempDir(), "out.mka")
	cleanPCM(t, ff, Input{Path: path, DurationMs: testSeconds * 1000}, Job{Pair: pair(2, 6), LagMs: m.LagMs, GainCap: GainCap(m.FrontGain), Out: out})
	got := decodeAll(t, ff, out, 0, 2)
	if len(got[0]) != len(vo[0]) {
		t.Fatalf("length %d, want %d", len(got[0]), len(vo[0]))
	}
	// what it should be: the voice-over without the original's centre (its
	// dialogue — and, as on 5.1, whatever else the centre carried)
	noCentre := make([][]float32, len(orig))
	for c := range orig {
		noCentre[c] = orig[c]
	}
	noCentre[2] = make([]float32, len(orig[2]))
	want := voiceOverStereo(noCentre, 0, false)
	for i := range want[0] { // its voice comes from voiceOverStereo itself: keep it out of the bed check below
		if voiceOn(i) {
			v := float32(0.7071 * russian(i))
			want[0][i] -= v
			want[1][i] -= v
		}
	}
	eng := make([]float32, len(orig[0]))
	for i := range eng {
		eng[i] = float32(english(i))
	}
	for c, name := range []string{"left", "right"} {
		a, b := int(8.5*sr), int(11.5*sr) // no voice speaks
		left := math.Abs(corr(got[c][a:b], eng[a:b])) * rms(got[c][a:b]) / rms(vo[c][a:b])
		d := make([]float32, b-a)
		for i := range d {
			d[i] = got[c][a+i] - want[c][a+i]
		}
		e := rms(d) / rms(want[c][a:b])
		t.Logf("%s: English left %.4f of the voice-over, error vs the bed without the centre %.3f", name, left, e)
		if left >= 0.02 {
			t.Errorf("%s: the English is still there: %.4f", name, left)
		}
		if e >= 0.25 {
			t.Errorf("%s: the music and effects are not kept: error %.3f", name, e)
		}
		// the voice is kept
		a, b = int(4*sr), int(6*sr)
		if lv := levelDB(got[c][a:b], vo[c][a:b]); lv < -6 {
			t.Errorf("%s: voice lost: %.1f dB", name, lv)
		}
	}
}
