package voclean

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/antresol-app/antresol/internal/media"
)

func ffmpegPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	return p
}

// testSeconds: the synthetic files' length.
const testSeconds = 20

// voiceOn: the synthetic "Russian voice" speaks in [3,7) and [12,16) s of
// every 20 s.
func voiceOn(i int) bool {
	t := math.Mod(float64(i)/sr, 20)
	return (t >= 3 && t < 7) || (t >= 12 && t < 16)
}

// english is the synthetic original dialogue (in the centre): a harmonic
// voice that speaks most of the time, with pauses.
func english(i int) float64 {
	t := float64(i) / sr
	if math.Mod(t, 5) > 4 { // a breath every 5 s
		return 0
	}
	return 0.06 * (0.6 + 0.4*math.Sin(2*math.Pi*2.7*t)) *
		(math.Sin(2*math.Pi*180*t) + 0.5*math.Sin(2*math.Pi*540*t) + 0.3*math.Sin(2*math.Pi*1620*t))
}

// russian is the synthetic voice of the voice-over or dub.
func russian(i int) float64 {
	t := float64(i) / sr
	return 0.07 * (0.5 + 0.5*math.Sin(2*math.Pi*4.1*t)) *
		(math.Sin(2*math.Pi*290*t) + 0.6*math.Sin(2*math.Pi*870*t) + 0.3*math.Sin(2*math.Pi*2610*t))
}

// musicEffects is a synthetic M&E (music and effects) bed of ch channels:
// independent music-like noise per channel.
func musicEffects(n, ch int, seed uint64) [][]float32 {
	rng := rand.New(rand.NewPCG(seed, 7))
	out := make([][]float32, ch)
	lp := make([]float64, ch)
	for c := range out {
		out[c] = make([]float32, n)
	}
	for i := range n {
		for c := range out {
			lp[c] = 0.92*lp[c] + 0.08*rng.NormFloat64()
			out[c][i] = float32(0.1*lp[c] + 0.01*rng.NormFloat64())
		}
	}
	return out
}

// original51 is a synthetic 5.1 original (FL FR FC LFE SL SR, n frames):
// the M&E bed with the English dialogue in the centre.
func original51(n int, seed uint64) [][]float32 { return originalN(n, 6, seed) }

func originalN(n, ch int, seed uint64) [][]float32 {
	o := musicEffects(n, ch, seed)
	for i := range n {
		o[2][i] += float32(english(i))
	}
	return o
}

// voiceOver51 lays a voice-over on orig the way the experiment measured
// LostFilm: L/R/LFE 0.47 × the original, surrounds the original inverted,
// the centre the ducked original (gain sliding 0.3 → 0.55) plus the voice.
// delay: the voice-over runs this many samples behind the original.
func voiceOver51(orig [][]float32, delay int) [][]float32 {
	n, ch := len(orig[0]), len(orig)
	vo := make([][]float32, ch)
	for c := range vo {
		vo[c] = make([]float32, n)
	}
	for i := range n {
		j := i - delay
		o := make([]float32, ch)
		if j >= 0 && j < n {
			for c := range o {
				o[c] = orig[c][j]
			}
		}
		for c := range ch {
			switch c {
			case 2:
			case 0, 1, 3:
				vo[c][i] = 0.47 * o[c]
			default:
				vo[c][i] = -o[c]
			}
		}
		g := 0.3 + 0.25*float64(i)/float64(n)
		vo[2][i] = float32(g) * o[2]
		if voiceOn(i) {
			vo[2][i] += float32(russian(i))
		}
	}
	return vo
}

// muxTracks writes the tracks (channel-major PCM) as audio streams 0, 1, …
// of a Matroska file (lossless float PCM: the tests compare samples).
func muxTracks(t *testing.T, ff string, tracks ...[][]float32) string {
	return muxFile(t, ff, "in.mkv", nil, tracks...)
}

// muxFile is muxTracks into name (its extension picks the container) with
// each track k starting delays[k] seconds late on the file's timeline
// (-itsoffset: the samples meant for time d+x play at d+x).
func muxFile(t *testing.T, ff, name string, delays []float64, tracks ...[][]float32) string {
	t.Helper()
	dir := t.TempDir()
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	for k, tr := range tracks {
		raw := filepath.Join(dir, "t"+strconv.Itoa(k)+".raw")
		var b bytes.Buffer
		for i := range tr[0] {
			for c := range tr {
				binary.Write(&b, binary.LittleEndian, tr[c][i])
			}
		}
		if err := os.WriteFile(raw, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		if k < len(delays) && delays[k] != 0 {
			// the raw demuxer ignores -itsoffset: a WAV of the samples first
			wav := filepath.Join(dir, "t"+strconv.Itoa(k)+".wav")
			if b, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "f32le", "-ar", "48000",
				"-ch_layout", layoutOf(len(tr)), "-i", raw, "-c:a", "pcm_f32le", wav).CombinedOutput(); err != nil {
				t.Fatalf("wav: %v: %s", err, b)
			}
			args = append(args, "-itsoffset", strconv.FormatFloat(delays[k], 'f', 3, 64), "-i", wav)
			continue
		}
		args = append(args, "-f", "f32le", "-ar", "48000", "-ch_layout", layoutOf(len(tr)), "-i", raw)
	}
	for k := range tracks {
		args = append(args, "-map", strconv.Itoa(k))
	}
	out := filepath.Join(dir, name)
	codec := "pcm_f32le"
	if strings.HasSuffix(name, ".nut") {
		codec = "pcm_s16le"
	}
	args = append(args, "-c:a", codec, "-metadata:s:a:0", "language=rus", "-metadata:s:a:0", "title=LostFilm", out)
	if b, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
		t.Fatalf("mux: %v: %s", err, b)
	}
	return out
}

func layoutOf(n int) string {
	switch n {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 8:
		return "7.1"
	}
	return "5.1(side)"
}

func stream(idx, ch int, lang string) media.AudioStream {
	return media.AudioStream{Index: idx, Codec: "pcm_f32le", Lang: lang, Title: map[string]string{"rus": "LostFilm"}[lang], Channels: ch, SampleRate: sr}
}

func pair(voCh, origCh int) Pair {
	return Pair{Vo: stream(0, voCh, "rus"), Orig: stream(1, origCh, "eng")}
}

// eligible1 measures one pair of a file.
func eligible1(t *testing.T, ff, path string, p Pair, durMs int64) Measure {
	t.Helper()
	ms, err := Eligible(context.Background(), ff, Input{Path: path, DurationMs: durMs}, []Pair{p})
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("%d measures", len(ms))
	}
	t.Logf("%+v", ms[0])
	return ms[0]
}

func TestEligibleVoiceOver(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(testSeconds*sr, 1)
	path := muxTracks(t, ff, voiceOver51(orig, 0), orig)
	m := eligible1(t, ff, path, pair(6, 6), testSeconds*1000)
	if !m.Eligible || math.Abs(m.LagMs) > 0.5 {
		t.Fatalf("a voice-over laid over the original: %+v", m)
	}
	if m.FrontResidual[0] > 0.05 || m.FrontResidual[1] > 0.05 || m.CentreResidual < 0.5 {
		t.Fatalf("residuals: %+v", m)
	}
	if m.DialogueGain < 0.1 || m.DialogueCoherence < 0.5 || m.DialogueFrames == 0 {
		t.Fatalf("the original's dialogue in the centre: %+v", m)
	}
	if math.Abs(m.FrontGain-0.47) > 0.02 { // voiceOver51: the fronts at 0.47× the original
		t.Fatalf("front gain %.3f, want 0.47", m.FrontGain)
	}
}

func TestEligibleDelayed40ms(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(testSeconds*sr, 2)
	path := muxTracks(t, ff, voiceOver51(orig, 40*sr/1000), orig)
	m := eligible1(t, ff, path, pair(6, 6), testSeconds*1000)
	if !m.Eligible || math.Abs(m.LagMs-40) > 0.5 {
		t.Fatalf("40 ms behind: %+v", m)
	}
}

func TestEligibleRejects(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(testSeconds*sr, 3)
	dub := original51(testSeconds*sr, 99) // independent: a full dub
	for name, vo := range map[string][][]float32{
		"dub":        dub,
		"2 s behind": voiceOver51(orig, 2*sr),
	} {
		path := muxTracks(t, ff, vo, orig)
		if m := eligible1(t, ff, path, pair(6, 6), testSeconds*1000); m.Eligible || m.Why == "" {
			t.Errorf("%s: eligible %+v", name, m)
		}
	}
	// mono and stereo: not eligible (stereo waits for the ML step: the
	// subtraction would take the music and effects with the English)
	for _, ch := range []int{1, 2} {
		o := orig[:ch]
		v := voiceOver51(orig, 0)[:ch]
		path := muxTracks(t, ff, v, o)
		if m := eligible1(t, ff, path, pair(ch, ch), testSeconds*1000); m.Eligible || m.Why == "" {
			t.Errorf("%d channels: %+v", ch, m)
		}
	}
	// a voice-over of another layout than its original
	path := muxTracks(t, ff, voiceOver51(orig, 0), orig[:2])
	if m := eligible1(t, ff, path, pair(6, 2), testSeconds*1000); m.Eligible || !strings.Contains(m.Why, "layout") {
		t.Errorf("layouts differ: %+v", m)
	}
}

// H2: a full dub made on the original's M&E — the fronts are the
// original's, the centre M&E plus Russian, but none of the original's
// dialogue — must not be cleaned (subtracting would add inverted English).
func TestEligibleRejectsDubOnOriginalME(t *testing.T) {
	ff := ffmpegPath(t)
	n := testSeconds * sr
	me := musicEffects(n, 6, 11)
	orig := make([][]float32, 6)
	dub := make([][]float32, 6)
	for c := range orig {
		orig[c] = append([]float32(nil), me[c]...)
		dub[c] = append([]float32(nil), me[c]...)
	}
	for i := range n {
		orig[2][i] += float32(english(i))
		dub[2][i] += float32(russian(i))
	}
	path := muxTracks(t, ff, dub, orig)
	m := eligible1(t, ff, path, pair(6, 6), testSeconds*1000)
	if m.Eligible || !strings.Contains(m.Why, "dialogue") {
		t.Fatalf("a dub on the original's M&E: %+v", m)
	}
}

// 7.1 has a centre: eligible like 5.1.
func TestEligible71(t *testing.T) {
	ff := ffmpegPath(t)
	orig := originalN(testSeconds*sr, 8, 12)
	path := muxTracks(t, ff, voiceOver51(orig, 0), orig)
	if m := eligible1(t, ff, path, pair(8, 8), testSeconds*1000); !m.Eligible {
		t.Fatalf("7.1: %+v", m)
	}
}

// voDialogue: a window's dialogue sums of a voice-over (coherence coh in
// every block).
func voDialogue(coh float64) dialogue {
	d := dialogue{frames: 400, cohNum: coh * 100, cohDen: 100, gainNum: 40, gainDen: 100}
	d.hist[min(int(coh*cohBins), cohBins-1)] = 25
	return d
}

// The windows' lags must agree within 3 ms (the refine read sets the
// lag to the sample).
func TestCombineLagSpread(t *testing.T) {
	ok := window{measured: true, residual: [3]float64{0.1, 0.1, 0.8}, dlg: voDialogue(0.9)}
	ws := []window{ok, ok, ok}
	ws[0].lag, ws[1].lag, ws[2].lag = 0, 144, 0 // 3 ms
	if m := combine(6, ws); !m.Eligible {
		t.Fatalf("3 ms spread rejected: %+v", m)
	}
	ws[1].lag = 168 // 3.5 ms
	if m := combine(6, ws); m.Eligible || !strings.Contains(m.Why, "drift") {
		t.Fatalf("3.5 ms spread: %+v", m)
	}
}

// The original's dialogue must come through (block p75 ≥ 0.5) in at least
// two of the three windows, and the mean coherence be ≥ 0.35.
func TestCombineDialoguePerWindow(t *testing.T) {
	w := func(coh float64) window {
		return window{measured: true, residual: [3]float64{0.1, 0.1, 0.8}, dlg: voDialogue(coh)}
	}
	for _, c := range []struct {
		cohs [3]float64
		want bool
	}{
		{[3]float64{0.9, 0.9, 0.2}, true},   // two windows, mean 0.67
		{[3]float64{0.95, 0.2, 0.2}, false}, // one window only (mean 0.45)
		{[3]float64{0.55, 0.55, 0.0}, true}, // two windows, mean 0.37
		{[3]float64{0.5, 0.5, 0.0}, false},  // mean 0.33 < 0.35
	} {
		m := combine(6, []window{w(c.cohs[0]), w(c.cohs[1]), w(c.cohs[2])})
		if m.Eligible != c.want {
			t.Errorf("%v: %+v", c.cohs, m)
		}
	}
}

// A dub that keeps the original's dialogue for one window's span only
// (the rest a dub on the M&E) is not a voice-over.
func TestEligibleDubKeepsOriginalOneWindow(t *testing.T) {
	ff := ffmpegPath(t)
	defer func(v int64) { measureWindowMs = v }(measureWindowMs)
	measureWindowMs = 10_000 // windows of 40 s: [6,16) [18,28) [30,40)
	const secs = 40
	n := secs * sr
	me := musicEffects(n, 6, 31)
	orig := make([][]float32, 6)
	for c := range orig {
		orig[c] = append([]float32(nil), me[c]...)
	}
	for i := range n {
		orig[2][i] += float32(english(i))
	}
	vo := voiceOver51(orig, 0) // fronts 0.47 × the original throughout
	for i := range n {
		if t := float64(i) / sr; t < 6 || t >= 16 { // a dub outside the first window
			vo[2][i] = 0.4*me[2][i] + float32(russian(i))
		}
	}
	path := muxTracks(t, ff, vo, orig)
	m := eligible1(t, ff, path, pair(6, 6), secs*1000)
	if m.Eligible || !strings.Contains(m.Why, "dialogue") {
		t.Fatalf("original kept in one window: %+v", m)
	}
}

// The refined lag is used only where the start correlates (normalised
// correlation ≥ 0.5); else the windows' lag stays.
func TestRefineLag(t *testing.T) {
	rng := rand.New(rand.NewPCG(5, 5))
	b := make([]float32, 30*sr)
	for i := range b {
		b[i] = float32(rng.NormFloat64())
	}
	a := make([]float32, len(b))
	for i := 5; i < len(a); i++ {
		a[i] = 0.5 * b[i-5] // 5 samples late
	}
	if lag, c := refineLag(a, b, 0); lag != 5 || c < 0.9 {
		t.Fatalf("correlated: lag %d corr %.2f", lag, c)
	}
	other := make([]float32, len(b))
	for i := range other {
		other[i] = float32(rng.NormFloat64())
	}
	if lag, c := refineLag(other, b, 7); lag != 7 || c >= 0.5 {
		t.Fatalf("uncorrelated start: lag %d corr %.2f, want the windows' 7", lag, c)
	}
}

// fakeFFmpeg writes a script standing in for ffmpeg.
func fakeFFmpeg(t *testing.T, script string) string {
	t.Helper()
	fake := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return fake
}

// stalls runs Eligible and Clean on a fake ffmpeg and returns their errors.
func stalls(t *testing.T, fake string) (error, error) {
	t.Helper()
	defer func(v time.Duration) { stallAfter = v }(stallAfter)
	stallAfter = 500 * time.Millisecond
	start := time.Now()
	_, err := Eligible(context.Background(), fake, Input{Path: "/dev/null", DurationMs: 20_000}, []Pair{pair(6, 6)})
	errs := Clean(context.Background(), fake, Input{Path: "/dev/null", DurationMs: 20_000},
		[]Job{{Pair: pair(6, 6), Out: filepath.Join(t.TempDir(), "o.mka")}})
	if el := time.Since(start); el > 20*time.Second {
		t.Fatalf("took %v", el)
	}
	return err, errs[0]
}

// One stream runs ahead until its queue is full while the read waits on
// another that never comes: the file is badly interleaved.
func TestStallBadlyInterleaved(t *testing.T) {
	// stream 0 (fd 3) far more than its queue holds, stream 1 (fd 4) nothing
	fake := fakeFFmpeg(t, "head -c 50000000 /dev/zero >&3\nexec sleep 30")
	e1, e2 := stalls(t, fake)
	if !errors.Is(e1, ErrBadlyInterleaved) || !errors.Is(e2, ErrBadlyInterleaved) {
		t.Fatalf("eligible %v, clean %v", e1, e2)
	}
}

// A read that stalls with no queue full (the disk starved at idle I/O
// priority, a hung ffmpeg) is an ordinary failure, not a verdict on the
// file.
func TestStallNotInterleaving(t *testing.T) {
	// one block on stream 0, then nothing on any while running
	fake := fakeFFmpeg(t, "head -c 98304 /dev/zero >&3\nexec sleep 30")
	e1, e2 := stalls(t, fake)
	for _, err := range []error{e1, e2} {
		if !errors.Is(err, ErrStalled) || errors.Is(err, ErrBadlyInterleaved) {
			t.Fatalf("eligible %v, clean %v", e1, e2)
		}
	}
}

// One track's encoder failing leaves the others of the same read alone.
func TestCleanEncoderFailureIsolated(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(5*sr, 32)
	path := muxTracks(t, ff, voiceOver51(orig, 0), orig, voiceOver51(orig, 0))
	dir := t.TempDir()
	good, bad := filepath.Join(dir, "good.mka"), filepath.Join(dir, "bad.mka")
	if err := os.Mkdir(bad+".tmp", 0o755); err != nil { // the encoder cannot write
		t.Fatal(err)
	}
	errs := clean(context.Background(), ff, Input{Path: path, DurationMs: 5000}, []Job{
		{Pair: Pair{Vo: stream(0, 6, "rus"), Orig: stream(1, 6, "eng")}, Out: good},
		{Pair: Pair{Vo: stream(2, 6, "rus"), Orig: stream(1, 6, "eng")}, Out: bad},
	}, pcmCodec)
	if errs[0] != nil || errs[1] == nil {
		t.Fatalf("errs %v", errs)
	}
	if got := decodeAll(t, ff, good, 0, 6); len(got[0]) != 5*sr {
		t.Fatalf("good track: %d samples", len(got[0]))
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatalf("bad track exists: %v", err)
	}
}

// Several voice-overs of a file are measured from one read.
func TestEligibleSeveralTracks(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(testSeconds*sr, 13)
	path := muxTracks(t, ff, voiceOver51(orig, 0), orig, original51(testSeconds*sr, 77), voiceOver51(orig, 40*sr/1000))
	ms, err := Eligible(context.Background(), ff, Input{Path: path, DurationMs: testSeconds * 1000}, []Pair{
		{Vo: stream(0, 6, "rus"), Orig: stream(1, 6, "eng")},
		{Vo: stream(2, 6, "rus"), Orig: stream(1, 6, "eng")},
		{Vo: stream(3, 6, "rus"), Orig: stream(1, 6, "eng")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ms[0].Eligible || ms[1].Eligible || !ms[2].Eligible || math.Abs(ms[2].LagMs-40) > 0.5 {
		t.Fatalf("%+v", ms)
	}
}

// decodeAll reads a stream of a file as channel-major float PCM.
func decodeAll(t *testing.T, ff, path string, idx, ch int) [][]float32 {
	t.Helper()
	b, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-i", path, "-map", "0:"+strconv.Itoa(idx),
		"-f", "f32le", "-ac", strconv.Itoa(ch), "-ar", "48000", "pipe:1").Output()
	if err != nil {
		t.Fatal(err)
	}
	n := len(b) / 4 / ch
	out := make([][]float32, ch)
	for c := range out {
		out[c] = make([]float32, n)
	}
	for i := range n {
		for c := range ch {
			out[c][i] = math.Float32frombits(binary.LittleEndian.Uint32(b[(i*ch+c)*4:]))
		}
	}
	return out
}

func corr(a, b []float32) float64 {
	var ab, aa, bb float64
	for i := range a {
		ab += float64(a[i]) * float64(b[i])
		aa += float64(a[i]) * float64(a[i])
		bb += float64(b[i]) * float64(b[i])
	}
	return ab / math.Sqrt(aa*bb)
}

func levelDB(x, ref []float32) float64 { return 20 * math.Log10(rms(x)/rms(ref)) }

func cleanPCM(t *testing.T, ff string, in Input, jobs ...Job) {
	t.Helper()
	for _, err := range clean(context.Background(), ff, in, jobs, pcmCodec) {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, j := range jobs {
		if _, err := os.Stat(j.Out + ".tmp"); !os.IsNotExist(err) {
			t.Fatalf("temp file left: %v", err)
		}
	}
}

func TestCleanPCM(t *testing.T) {
	ff := ffmpegPath(t)
	for _, delay := range []int{0, 40} {
		orig := original51(testSeconds*sr, 5)
		vo := voiceOver51(orig, delay*sr/1000)
		path := muxTracks(t, ff, vo, orig)
		out := filepath.Join(t.TempDir(), "out.mka")
		cleanPCM(t, ff, Input{Path: path, DurationMs: testSeconds * 1000}, Job{Pair: pair(6, 6), LagMs: float64(delay), Out: out})
		got := decodeAll(t, ff, out, 0, 6)
		if d := len(got[0]) - len(vo[0]); d != 0 {
			t.Fatalf("delay %d: length %d, want %d", delay, len(got[0]), len(vo[0]))
		}
		for _, c := range []int{0, 1, 3, 4, 5} { // copied bit-exact
			for i := range got[c] {
				if math.Abs(float64(got[c][i]-vo[c][i])) > 1e-3 {
					t.Fatalf("delay %d: channel %d sample %d changed: %g -> %g", delay, c, i, vo[c][i], got[c][i])
				}
			}
		}
		// the centre where no voice speaks: the original gone
		a, b := int(8.5*sr), int(11.5*sr)
		// what is left of the original, relative to the voice-over's level
		// (the correlation of a −60 dB residue says nothing on its own)
		left := math.Abs(corr(got[2][a:b], orig[2][a-delay*sr/1000:b-delay*sr/1000])) * rms(got[2][a:b]) / rms(vo[2][a:b])
		lvl := levelDB(got[2][a:b], vo[2][a:b])
		t.Logf("delay %d ms: voice-free centre: original left %.5f of the voice-over, level %.1f dB", delay, left, lvl)
		if left >= 0.01 || lvl > -30 {
			t.Fatalf("delay %d: centre still holds the original: %.4f left, level %.1f dB", delay, left, lvl)
		}
		// and the voice is kept
		a, b = int(4*sr), int(6*sr)
		if lv := levelDB(got[2][a:b], vo[2][a:b]); lv < -6 {
			t.Fatalf("delay %d: voice lost: %.1f dB", delay, lv)
		}
	}
}

// H1: a voice-over stream that starts 40 ms late on the file's timeline
// (a container delay, samples aligned to the original on that timeline)
// is measured and cleaned on the same timeline. 70 s: the windows start
// after the file's start (input -ss), and the cleaning reads from 0.
func TestCleanContainerDelay(t *testing.T) {
	ff := ffmpegPath(t)
	const secs = 70
	orig := original51(secs*sr, 21)
	full := voiceOver51(orig, 0)
	// the stream holds the voice-over from 40 ms on, placed at 40 ms
	d := 40 * sr / 1000
	vo := make([][]float32, 6)
	for c := range vo {
		vo[c] = full[c][d:]
	}
	path := muxFile(t, ff, "in.mkv", []float64{0.040, 0}, vo, orig)
	if b, err := exec.Command(strings.TrimSuffix(ff, "ffmpeg")+"ffprobe", "-v", "error", "-show_entries", "stream=start_time",
		"-of", "csv=p=0", path).Output(); err == nil && !strings.HasPrefix(string(b), "0.040") {
		t.Fatalf("fixture: stream starts %q, want 0.040", b)
	}
	in := Input{Path: path, DurationMs: secs * 1000}
	m := eligible1(t, ff, path, pair(6, 6), secs*1000)
	if !m.Eligible || math.Abs(m.LagMs) > 0.5 {
		t.Fatalf("container delay: %+v", m)
	}
	out := filepath.Join(t.TempDir(), "out.mka")
	cleanPCM(t, ff, in, Job{Pair: pair(6, 6), LagMs: m.LagMs, Out: out})
	got := decodeAll(t, ff, out, 0, 6)
	// the cleaned track is on the file's timeline from 0 (its first 40 ms
	// silent): compare with the whole voice-over
	a, b := int(28.5*sr), int(31.5*sr) // the voice silent, English speaking
	lvl := levelDB(got[2][a:b], full[2][a:b])
	t.Logf("English under no voice: %.1f dB", lvl)
	if lvl > -25 {
		t.Fatalf("English suppressed only %.1f dB", -lvl)
	}
}

// Two voice-overs cleaned from one read, each into its own file.
func TestCleanSeveralTracks(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(testSeconds*sr, 22)
	v0, v2 := voiceOver51(orig, 0), voiceOver51(orig, 40*sr/1000)
	path := muxTracks(t, ff, v0, orig, v2)
	dir := t.TempDir()
	o0, o2 := filepath.Join(dir, "a.mka"), filepath.Join(dir, "b.mka")
	cleanPCM(t, ff, Input{Path: path, DurationMs: testSeconds * 1000},
		Job{Pair: Pair{Vo: stream(0, 6, "rus"), Orig: stream(1, 6, "eng")}, LagMs: 0, Out: o0},
		Job{Pair: Pair{Vo: stream(2, 6, "rus"), Orig: stream(1, 6, "eng")}, LagMs: 40, Out: o2})
	a, b := int(8.5*sr), int(11.5*sr)
	for out, vo := range map[string][][]float32{o0: v0, o2: v2} {
		got := decodeAll(t, ff, out, 0, 6)
		if lvl := levelDB(got[2][a:b], vo[2][a:b]); lvl > -30 {
			t.Fatalf("%s: %.1f dB", out, lvl)
		}
	}
}

// Output samples are clipped to [-1, 1] before the encoder.
func TestCleanClips(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(5*sr, 23)
	vo := voiceOver51(orig, 0)
	for i := range vo[0] {
		vo[0][i] = 1.5
	}
	path := muxTracks(t, ff, vo, orig)
	out := filepath.Join(t.TempDir(), "o.mka")
	cleanPCM(t, ff, Input{Path: path, DurationMs: 5000}, Job{Pair: pair(6, 6), Out: out})
	got := decodeAll(t, ff, out, 0, 6)
	for i, v := range got[0] {
		if v > 1 || v < -1 {
			t.Fatalf("sample %d = %g", i, v)
		}
	}
}

// Only the known video containers are read (a downloaded file named .mkv
// could be anything).
func TestFormatWhitelist(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(5*sr, 24)
	path := muxFile(t, ff, "in.nut", nil, voiceOver51(orig, 0), orig)
	if _, err := Eligible(context.Background(), ff, Input{Path: path, DurationMs: 5000}, []Pair{pair(6, 6)}); err == nil {
		t.Fatal("a NUT file was read")
	}
}

// A gated job is held while playback reads the disk, then completes.
func TestCleanYieldsToPlayback(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(5*sr, 25)
	path := muxTracks(t, ff, voiceOver51(orig, 0), orig)
	until := time.Now().Add(1200 * time.Millisecond)
	busy := func() bool { return time.Now().Before(until) }
	out := filepath.Join(t.TempDir(), "o.mka")
	start := time.Now()
	cleanPCM(t, ff, Input{Path: path, DurationMs: 5000, Busy: busy}, Job{Pair: pair(6, 6), Out: out})
	if el := time.Since(start); el < time.Second {
		t.Fatalf("done in %v while playback was busy", el)
	}
	if got := decodeAll(t, ff, out, 0, 6); len(got[0]) != 5*sr {
		t.Fatalf("length %d", len(got[0]))
	}
}

func TestCleanEAC3(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(5*sr, 6)
	path := muxTracks(t, ff, voiceOver51(orig, 0), orig)
	out := filepath.Join(t.TempDir(), "audio", "7-0.mka")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Clean(context.Background(), ff, Input{Path: path, DurationMs: 5000}, []Job{{Pair: pair(6, 6), Out: out}})[0]; err != nil {
		t.Fatal(err)
	}
	probe, err := exec.Command(strings.TrimSuffix(ff, "ffmpeg")+"ffprobe", "-v", "error", "-show_entries",
		"stream=codec_name,channels,bit_rate:stream_tags=language,title:format=duration", "-of", "flat", out).Output()
	if err != nil {
		t.Skipf("no ffprobe beside ffmpeg: %v", err)
	}
	p := string(probe)
	t.Log(p)
	for _, want := range []string{`codec_name="eac3"`, `channels=6`, `language="rus"`, `title="LostFilm (очищено)"`} {
		if !strings.Contains(p, want) {
			t.Errorf("probe lacks %s", want)
		}
	}
	var dur float64
	for _, l := range strings.Split(p, "\n") {
		if v, ok := strings.CutPrefix(l, "format.duration="); ok {
			dur, _ = strconv.ParseFloat(strings.Trim(v, `"`), 64)
		}
	}
	if math.Abs(dur-5) > 0.1 {
		t.Errorf("duration %.3f s, want 5 ± 0.1", dur)
	}
}

func TestCleanFailureRemovesTemp(t *testing.T) {
	ff := ffmpegPath(t)
	orig := original51(2*sr, 8)
	path := muxTracks(t, ff, voiceOver51(orig, 0), orig)
	// the encoder cannot write: a directory is where the temp file goes
	out := filepath.Join(t.TempDir(), "x.mka")
	if err := os.Mkdir(out+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Clean(context.Background(), ff, Input{Path: path, DurationMs: 2000}, []Job{{Pair: pair(6, 6), Out: out}})[0]; err == nil {
		t.Fatal("no error")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("output exists: %v", err)
	}
	// a missing input fails too, nothing left
	out2 := filepath.Join(t.TempDir(), "y.mka")
	if err := Clean(context.Background(), ff, Input{Path: path + ".gone", DurationMs: 2000}, []Job{{Pair: pair(6, 6), Out: out2}})[0]; err == nil {
		t.Fatal("no error for a missing file")
	}
	if m, _ := filepath.Glob(filepath.Dir(out2) + "/*"); len(m) != 0 {
		t.Fatalf("left: %v", m)
	}
}
