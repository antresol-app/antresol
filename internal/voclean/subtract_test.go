package voclean

import (
	"math"
	"math/cmplx"
	"math/rand/v2"
	"testing"

	"github.com/antresol-app/antresol/internal/dsp"
)

const sr = 48000

// synth builds the experiment's situation: orig = music-like noise plus
// "English speech" (amplitude-modulated tones), voice = "Russian speech"
// (other modulated tones, gated: silent in the voice-free stretches), and
// vo = g(t)·orig + voice with g sliding 0.3 → 0.55. speaking reports
// whether the voice is on at sample i.
func synth(seconds int) (vo, orig, voice []float32, speaking func(i int) bool) {
	n := seconds * sr
	rng := rand.New(rand.NewPCG(1, 2))
	vo, orig, voice = make([]float32, n), make([]float32, n), make([]float32, n)
	// voice on in [2,6) s, [9,13) s, [16,…) s of every 20 s
	speaking = func(i int) bool {
		t := float64(i%(20*sr)) / sr
		return (t >= 2 && t < 6) || (t >= 9 && t < 13) || t >= 16
	}
	var lp float64
	for i := range n {
		t := float64(i) / sr
		lp = 0.9*lp + 0.1*rng.NormFloat64() // pinkish: more low end, like music
		music := 0.08*lp + 0.01*rng.NormFloat64()
		eng := 0.05 * (0.6 + 0.4*math.Sin(2*math.Pi*3.1*t)) *
			(math.Sin(2*math.Pi*220*t) + 0.5*math.Sin(2*math.Pi*660*t) + 0.25*math.Sin(2*math.Pi*1320*t))
		orig[i] = float32(music + eng)
		if speaking(i) {
			voice[i] = float32(0.06 * (0.5 + 0.5*math.Sin(2*math.Pi*4.3*t)) *
				(math.Sin(2*math.Pi*310*t) + 0.6*math.Sin(2*math.Pi*930*t) + 0.3*math.Sin(2*math.Pi*2790*t)))
		}
		g := 0.3 + 0.25*float64(i)/float64(n)
		vo[i] = float32(g*float64(orig[i])) + voice[i]
	}
	return vo, orig, voice, speaking
}

// run streams vo/orig through a Subtractor in blocks of block samples (0:
// the whole buffer at once).
func run(vo, orig []float32, block int) []float32 {
	s := NewSubtractor(sr)
	if block <= 0 {
		block = len(vo)
	}
	var out []float32
	for i := 0; i < len(vo); i += block {
		j := min(i+block, len(vo))
		out = append(out, s.Process(vo[i:j], orig[i:j])...)
	}
	return append(out, s.Flush()...)
}

func rms(x []float32) float64 {
	var s float64
	for _, v := range x {
		s += float64(v) * float64(v)
	}
	return math.Sqrt(s / float64(max(len(x), 1)))
}

func TestFFTRoundTrip(t *testing.T) {
	f := dsp.NewFFT(16)
	in := make([]complex128, 16)
	for i := range in {
		in[i] = complex(float64(i%5)-2, float64(i%3))
	}
	// against the definition
	want := make([]complex128, 16)
	for k := range want {
		for n, v := range in {
			want[k] += v * cmplx.Exp(complex(0, -2*math.Pi*float64(k*n)/16))
		}
	}
	got := append([]complex128(nil), in...)
	f.Transform(got, false)
	for k := range got {
		if cmplx.Abs(got[k]-want[k]) > 1e-9 {
			t.Fatalf("bin %d: %v, want %v", k, got[k], want[k])
		}
	}
	f.Transform(got, true)
	for k := range got {
		if cmplx.Abs(got[k]/16-in[k]) > 1e-12 {
			t.Fatalf("inverse %d: %v, want %v", k, got[k]/16, in[k])
		}
	}
}

func TestSubtractorRemovesOriginal(t *testing.T) {
	vo, orig, voice, speaking := synth(20)
	out := run(vo, orig, 4096)
	if len(out) != len(vo) {
		t.Fatalf("length %d, want %d", len(out), len(vo))
	}
	diff := make([]float32, len(out))
	for i := range out {
		diff[i] = out[i] - voice[i]
	}
	r := rms(diff) / rms(voice)
	t.Logf("rms(out-voice)/rms(voice) = %.4f", r)
	if r >= 0.1 {
		t.Fatalf("rms(out-voice)/rms(voice) = %.3f, want < 0.1", r)
	}
	// voice-free stretches (away from the on/off edges by the gain
	// window): ≥ 30 dB below the voice-over
	for _, span := range [][2]float64{{0.5, 1.5}, {6.5, 8.5}, {13.5, 15.5}} {
		a, b := int(span[0]*sr), int(span[1]*sr)
		if speaking(a) || speaking(b) {
			t.Fatalf("span %v is not voice-free", span)
		}
		db := 20 * math.Log10(rms(out[a:b])/rms(vo[a:b]))
		t.Logf("voice-free %v s: %.1f dB", span, db)
		if db > -30 {
			t.Errorf("voice-free %v s: %.1f dB below the voice-over, want ≤ -30", span, db)
		}
	}
}

func TestSubtractorBlockSizeIndependent(t *testing.T) {
	vo, orig, _, _ := synth(6)
	whole := run(vo, orig, 0)
	for _, block := range []int{1000, 48000, 1, 513} {
		got := run(vo, orig, block)
		if len(got) != len(whole) {
			t.Fatalf("block %d: length %d, want %d", block, len(got), len(whole))
		}
		var worst float64
		for i := range got {
			worst = max(worst, math.Abs(float64(got[i]-whole[i])))
		}
		if worst >= 1e-4 {
			t.Fatalf("block %d: max abs diff %g from the whole-buffer output", block, worst)
		}
	}
}

func TestSubtractorLengthAndAlignment(t *testing.T) {
	// orig silent: nothing to subtract, the output is the input, aligned
	for _, n := range []int{0, 1, 100, 2047, 2048, 2049, 48000 + 17} {
		vo := make([]float32, n)
		for i := range vo {
			vo[i] = float32(math.Sin(float64(i) * 0.01))
		}
		out := run(vo, make([]float32, n), 777)
		if len(out) != n {
			t.Fatalf("n=%d: length %d", n, len(out))
		}
		for i := range out {
			if math.Abs(float64(out[i]-vo[i])) > 1e-5 {
				t.Fatalf("n=%d: sample %d = %g, want %g", n, i, out[i], vo[i])
			}
		}
	}
}

func TestSubtractorBoundedMemory(t *testing.T) {
	s := NewSubtractor(sr)
	rng := rand.New(rand.NewPCG(3, 4))
	block := 4800
	vo, orig := make([]float32, block), make([]float32, block)
	start := -1
	total := 0
	for i := 0; i < 10*60*sr/block; i++ { // 10 minutes
		for j := range vo {
			orig[j] = float32(rng.NormFloat64() * 0.1)
			vo[j] = 0.5*orig[j] + float32(rng.NormFloat64()*0.01)
		}
		total += len(s.Process(vo, orig))
		if i == 10 {
			start = s.bufferedCap()
		}
		if i > 10 && s.bufferedCap() != start {
			t.Fatalf("after %d s: buffers hold %d values, %d after the first second", (i+1)*block/sr, s.bufferedCap(), start)
		}
	}
	total += len(s.Flush())
	if total != 10*60*sr {
		t.Fatalf("emitted %d samples, want %d", total, 10*60*sr)
	}
}

// A voice-over mixed louder than its original (Кубик в Кубе: 2.45× the
// English track) carries the original's centre above the experiment's
// bound of 1.5: the bound follows the voice-over's level (GainCap).
func TestSubtractorLoudVoiceOver(t *testing.T) {
	vo, orig, voice, speaking := synth(20)
	for i := range vo {
		vo[i] = (vo[i]-voice[i])*2.45/0.55 + voice[i] // the original at up to 2.45×
	}
	clean := func(capGain float64) []float32 {
		s := NewSubtractor(sr)
		s.SetGainCap(capGain)
		return append(s.Process(vo, orig), s.Flush()...)
	}
	level := func(out []float32) float64 {
		a, b := int(6.5*sr), int(8.5*sr)
		if speaking(a) || speaking(b) {
			t.Fatal("span not voice-free")
		}
		return 20 * math.Log10(rms(out[a:b])/rms(vo[a:b]))
	}
	if db := level(clean(gainMax)); db < -20 {
		t.Fatalf("the fixed bound already removes it (%.1f dB): the test proves nothing", db)
	}
	if db := level(clean(GainCap(2.45))); db > -30 {
		t.Fatalf("with the bound at the voice-over's level: %.1f dB, want ≤ -30", db)
	}
	if GainCap(0.47) != gainMax || GainCap(2) != 2*gainMax || GainCap(50) != maxFrontGain*gainMax || GainCap(-1) != gainMax {
		t.Fatalf("GainCap(0.47)=%v GainCap(2)=%v", GainCap(0.47), GainCap(2))
	}
}
