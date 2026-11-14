package voclean

import (
	"math"
	"math/rand/v2"
	"testing"
)

// stereoSynth: a 5.1 original's centre (English speech plus music) and
// left front (music correlated with the centre's, and its own), and a
// stereo voice-over's left channel: g·(FL + 0.707·FC) + 0.707·voice. want
// is what a cleaned left channel should be: g·FL + 0.707·voice.
func stereoSynth(seconds int) (voL, fc, fl, want []float32) {
	vo, orig, voice, _ := synth(seconds)
	n := len(vo)
	rng := rand.New(rand.NewPCG(3, 4))
	voL, fc, fl, want = make([]float32, n), make([]float32, n), make([]float32, n), make([]float32, n)
	var lp float64
	for i := range n {
		lp = 0.95*lp + 0.05*rng.NormFloat64()
		g := 0.3 + 0.25*float64(i)/float64(n)
		fc[i] = orig[i]                                 // music + English
		fl[i] = float32(0.7*float64(orig[i]) + 0.1*lp)  // the centre's music bleeds into the front (and its English, a little)
		fl[i] -= float32(0.7) * float32(engOf(orig, i)) // …but the front carries no dialogue
		voL[i] = float32(g*(float64(fl[i])+0.707*float64(fc[i]))) + 0.707*voice[i]
		want[i] = float32(g*float64(fl[i])) + 0.707*voice[i]
		_ = vo
	}
	return voL, fc, fl, want
}

// engOf is the English part of synth's original at sample i (its tones).
func engOf(_ []float32, i int) float64 {
	t := float64(i) / sr
	return 0.05 * (0.6 + 0.4*math.Sin(2*math.Pi*3.1*t)) *
		(math.Sin(2*math.Pi*220*t) + 0.5*math.Sin(2*math.Pi*660*t) + 0.25*math.Sin(2*math.Pi*1320*t))
}

func TestSubtractor2KeepsTheFront(t *testing.T) {
	voL, fc, fl, want := stereoSynth(20)
	two := NewSubtractor2(sr)
	var out2 []float32
	for i := 0; i < len(voL); i += 4096 {
		j := min(i+4096, len(voL))
		out2 = append(out2, two.Process2(voL[i:j], fc[i:j], fl[i:j])...)
	}
	out2 = append(out2, two.Flush()...)
	out1 := run(voL, fc, 4096)
	if len(out2) != len(voL) {
		t.Fatalf("length %d, want %d", len(out2), len(voL))
	}
	errOf := func(out []float32) float64 {
		d := make([]float32, len(out))
		for i := range out {
			d[i] = out[i] - want[i]
		}
		return rms(d) / rms(want)
	}
	e1, e2 := errOf(out1), errOf(out2)
	t.Logf("error vs the clean left: one reference %.3f, two %.3f", e1, e2)
	if e2 >= 0.15 {
		t.Errorf("two references: error %.3f, want < 0.15", e2)
	}
	if e2 >= e1 {
		t.Errorf("two references (%.3f) no better than one (%.3f)", e2, e1)
	}
	// the English is gone: what is left of it, correlated with the speech
	var num, den float64
	for i := range out2 {
		e := engOf(nil, i)
		num += float64(out2[i]-want[i]) * e
		den += e * e
	}
	if leak := math.Abs(num / den); leak > 0.05 {
		t.Errorf("English left in the cleaned left: %.3f of it", leak)
	}
}
