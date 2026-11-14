// Package voclean makes «cleaned» voice-overs (stage C of the voice-over
// design): a Russian
// voice-over laid over the ducked original of the same master has the
// original subtracted, so only the Russian voices and the rest of the
// soundtrack stay. The method is the experiment's reference subtraction
// (testdata/reference/subtract.py): per STFT band and ~0.3 s window a
// least-squares gain of the original in the voice-over, subtracted in the
// complex spectrum.
package voclean

import (
	"math"

	"github.com/antresol-app/antresol/internal/dsp"
)

const (
	// stftSize and stftHop: the experiment's STFT (Hann, 2048/512).
	stftSize = 2048
	stftHop  = 512
	// bands: log-spaced bands of the gain (fewer after merging the
	// duplicate low edges, as numpy's unique does).
	bands = 64
	// gainWindow: the centred window of frames a band's gain is fitted
	// over, in seconds.
	gainWindow = 0.3
	// gainMax clips the fitted gain (the experiment's [0, 1.5]).
	gainMax = 1.5
	// denFloor keeps the gain finite where the original is silent: the
	// experiment's 1e-12 on scipy's 1/sum(window)-scaled spectra.
	denFloor = 1e-12 * (stftSize / 2) * (stftSize / 2)
	// olaNorm: Σ w² of a periodic Hann at hop n/4 (constant 1.5).
	olaNorm = 1.5
)

// frame is one analysed STFT frame: both spectra (bins 0…n/2) and the
// per-band sums of the gain fit.
type frame struct {
	x, y     []complex128
	num, den []float64
	// the second reference (Subtractor2): its spectrum and the band sums
	// Re Σ X·Ȳ2, Σ |Y2|², Re Σ Y·Ȳ2
	y2              []complex128
	num2, den2, y12 []float64
}

// Subtractor removes a reference (the original) from a signal that holds
// it scaled by a slowly varying gain per band (the voice-over): out = X −
// g·Y in the STFT, g = Re Σ X·Ȳ / Σ |Y|² over the band's bins and a centred
// window of frames, clipped to [0, gainMax]. It streams: Process takes any
// block sizes and keeps a fixed amount of state (a window of frames), so a
// whole film never sits in memory, and the output does not depend on how
// the input was cut into blocks.
type Subtractor struct {
	n, hop      int
	back, ahead int // gain window: frames before and after the frame
	window      []float64
	edges       []int // band b: bins [edges[b], edges[b+1])
	f           *dsp.FFT

	bufVo, bufOr []float64 // the frame being filled; fill samples in it
	fill         int
	ring         []frame // the last back+ahead+1 frames, by index mod len
	analysed     int     // frames analysed
	synthesised  int     // frames synthesised (overlap-added)
	acc          []float64
	z            []complex128 // FFT scratch
	in, out      int64        // samples taken, samples emitted
	gainCap      float64      // the fitted gain's upper bound (gainMax unless set)
	// two: a second reference (NewSubtractor2) fitted together with the
	// first, only the first subtracted
	two    bool
	bufOr2 []float64
	z2     []complex128
}

// GainCap returns the upper bound of the fitted gain for a voice-over whose
// fronts are the original scaled by frontGain (Measure.FrontGain): gainMax
// of the original's level. A voice-over mixed louder than its original
// (Кубик в Кубе: 2.45×) carries the original's centre above gainMax, and a
// fixed bound left a third of the English in.
func GainCap(frontGain float64) float64 { return gainMax * min(max(1, frontGain), maxFrontGain) }

// maxFrontGain: a voice-over mixed more than this many times louder than
// its original is not taken at its word (a nearly silent original's noisy
// fit would let the bound run away)
const maxFrontGain = 6

// SetGainCap sets the fitted gain's upper bound (GainCap); before any input.
func (s *Subtractor) SetGainCap(c float64) {
	if c > 0 {
		s.gainCap = c
	}
}

// NewSubtractor returns a Subtractor for audio at sampleRate (Hz).
func NewSubtractor(sampleRate int) *Subtractor {
	n, hop := stftSize, stftHop
	win := max(int(gainWindow*float64(sampleRate)/float64(hop)), 1)
	s := &Subtractor{n: n, hop: hop, back: win / 2, ahead: win - 1 - win/2,
		window: make([]float64, n), edges: bandEdges(n/2+1, bands), f: dsp.NewFFT(n),
		bufVo: make([]float64, n), bufOr: make([]float64, n),
		acc: make([]float64, n), z: make([]complex128, n)}
	for i := range s.window {
		s.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n)) // periodic Hann
	}
	s.gainCap = gainMax
	s.ring = make([]frame, s.back+s.ahead+1)
	nb := len(s.edges) - 1
	for i := range s.ring {
		s.ring[i] = frame{x: make([]complex128, n/2+1), y: make([]complex128, n/2+1),
			num: make([]float64, nb), den: make([]float64, nb)}
	}
	// frame 0 starts n-hop samples before the signal (zeros), so every
	// sample is covered by n/hop frames and the overlap-add is exact
	s.fill = n - hop
	return s
}

// NewSubtractor2 returns a Subtractor that fits two references together
// and subtracts only the first: a stereo voice-over over a 5.1 original,
// whose left channel holds the original's centre (the dialogue, to go)
// and its left front (music and effects, to stay) — fitted alone, the
// centre would take the part of the front it correlates with. Process2
// feeds it.
func NewSubtractor2(sampleRate int) *Subtractor {
	s := NewSubtractor(sampleRate)
	s.two = true
	s.bufOr2 = make([]float64, s.n)
	s.z2 = make([]complex128, s.n)
	nb := len(s.edges) - 1
	for i := range s.ring {
		s.ring[i].y2 = make([]complex128, s.n/2+1)
		s.ring[i].num2, s.ring[i].den2, s.ring[i].y12 = make([]float64, nb), make([]float64, nb), make([]float64, nb)
	}
	return s
}

// Process2 is Process with the second reference (NewSubtractor2).
func (s *Subtractor) Process2(vo, orig, orig2 []float32) []float32 {
	if len(vo) != len(orig) || len(vo) != len(orig2) {
		panic("voclean: Process2: blocks of different lengths")
	}
	s.in += int64(len(vo))
	out := make([]float32, 0, len(vo)+s.hop)
	for i := range vo {
		s.bufOr2[s.fill] = float64(orig2[i])
		out = s.push(float64(vo[i]), float64(orig[i]), out)
	}
	return out
}

// bandEdges: numpy's unique(round(geomspace(1, bins, nb+1))) with the
// first edge 0.
func bandEdges(bins, nb int) []int {
	var e []int
	for i := 0; i <= nb; i++ {
		v := int(math.RoundToEven(math.Pow(float64(bins), float64(i)/float64(nb))))
		if len(e) == 0 || v != e[len(e)-1] {
			e = append(e, v)
		}
	}
	e[0] = 0
	e[len(e)-1] = bins
	return e
}

// Process takes the next samples of the voice-over and the original
// (equal lengths, aligned) and returns the cleaned samples complete so far:
// the output lags the input by the STFT and the gain window (≈ 0.17 s),
// Flush returns the rest. Over a whole stream the output has the input's
// length and is aligned to it.
func (s *Subtractor) Process(vo, orig []float32) []float32 {
	if len(vo) != len(orig) {
		panic("voclean: Process: blocks of different lengths")
	}
	s.in += int64(len(vo))
	out := make([]float32, 0, len(vo)+s.hop)
	for i := range vo {
		out = s.push(float64(vo[i]), float64(orig[i]), out)
	}
	return out
}

// Flush ends the stream and returns the remaining output. The Subtractor
// is not to be used afterwards.
func (s *Subtractor) Flush() []float32 {
	var out []float32
	// zeros past the end: frames of zeros add nothing to the gain sums, so
	// the last frames' windows are simply cut at the end
	for s.out < s.in {
		if s.two {
			s.bufOr2[s.fill] = 0
		}
		out = s.push(0, 0, out)
	}
	return out
}

func (s *Subtractor) push(vo, orig float64, out []float32) []float32 {
	s.bufVo[s.fill], s.bufOr[s.fill] = vo, orig
	s.fill++
	if s.fill < s.n {
		return out
	}
	s.analyse()
	copy(s.bufVo, s.bufVo[s.hop:])
	copy(s.bufOr, s.bufOr[s.hop:])
	if s.two {
		copy(s.bufOr2, s.bufOr2[s.hop:])
	}
	s.fill = s.n - s.hop
	if k := s.analysed - 1 - s.ahead; k >= 0 {
		out = s.synthesise(k, out)
	}
	return out
}

// analyse computes the spectra of the full frame in the buffers (both
// real signals in one complex FFT) and its band sums.
func (s *Subtractor) analyse() {
	n := s.n
	for i := range n {
		w := s.window[i]
		s.z[i] = complex(w*s.bufVo[i], w*s.bufOr[i])
	}
	s.f.Transform(s.z, false)
	fr := &s.ring[s.analysed%len(s.ring)]
	for k := 0; k <= n/2; k++ {
		a, b := s.z[k], s.z[(n-k)%n]
		bc := complex(real(b), -imag(b))
		fr.x[k] = (a + bc) / 2
		d := (a - bc) / 2 // = i·Y
		fr.y[k] = complex(imag(d), -real(d))
	}
	for b := range fr.num {
		var num, den float64
		for k := s.edges[b]; k < s.edges[b+1]; k++ {
			x, y := fr.x[k], fr.y[k]
			num += real(x)*real(y) + imag(x)*imag(y)
			den += real(y)*real(y) + imag(y)*imag(y)
		}
		fr.num[b], fr.den[b] = num, den
	}
	if s.two {
		for i := range n {
			s.z2[i] = complex(s.window[i]*s.bufOr2[i], 0)
		}
		s.f.Transform(s.z2, false)
		copy(fr.y2, s.z2[:n/2+1])
		for b := range fr.num2 {
			var num2, den2, y12 float64
			for k := s.edges[b]; k < s.edges[b+1]; k++ {
				x, y, y2 := fr.x[k], fr.y[k], fr.y2[k]
				num2 += real(x)*real(y2) + imag(x)*imag(y2)
				den2 += real(y2)*real(y2) + imag(y2)*imag(y2)
				y12 += real(y)*real(y2) + imag(y)*imag(y2)
			}
			fr.num2[b], fr.den2[b], fr.y12[b] = num2, den2, y12
		}
	}
	s.analysed++
}

// gainOf fits the band's gain of the (first) reference over the window's
// sums: alone, or together with the second reference (2×2 least squares,
// a little ridge so a reference silent in the band does not blow it up;
// near-collinear references fall back to the first alone).
func (s *Subtractor) gainOf(num, den, num2, den2, y12 float64) float64 {
	g := num / max(den, denFloor)
	if s.two {
		r := 1e-3 * (den + den2)
		a, d := den+r, den2+r
		if det := a*d - y12*y12; det > 1e-6*a*d && det > 0 {
			g = (num*d - num2*y12) / det
		}
	}
	return min(max(g, 0), s.gainCap)
}

// synthesise subtracts the fitted original from frame k, overlap-adds it
// and emits the hop of samples it completes.
func (s *Subtractor) synthesise(k int, out []float32) []float32 {
	n, hop := s.n, s.hop
	lo, hi := max(k-s.back, 0), min(k+s.ahead, s.analysed-1)
	fr := &s.ring[k%len(s.ring)]
	for b := range fr.num {
		var num, den, num2, den2, y12 float64
		for j := lo; j <= hi; j++ {
			o := &s.ring[j%len(s.ring)]
			num += o.num[b]
			den += o.den[b]
			if s.two {
				num2 += o.num2[b]
				den2 += o.den2[b]
				y12 += o.y12[b]
			}
		}
		g := s.gainOf(num, den, num2, den2, y12)
		for i := s.edges[b]; i < s.edges[b+1]; i++ {
			s.z[i] = fr.x[i] - complex(g, 0)*fr.y[i]
		}
	}
	for i := 1; i < n/2; i++ {
		v := s.z[i]
		s.z[n-i] = complex(real(v), -imag(v))
	}
	s.f.Transform(s.z, true)
	for i := range n {
		s.acc[i] += s.window[i] * real(s.z[i]) / float64(n)
	}
	// samples [start, start+hop) are complete: no later frame covers them
	start := int64(k)*int64(hop) - int64(n-hop)
	for i := range hop {
		if p := start + int64(i); p >= 0 && p < s.in {
			out = append(out, float32(s.acc[i]/olaNorm))
			s.out++
		}
	}
	copy(s.acc, s.acc[hop:])
	clear(s.acc[n-hop:])
	s.synthesised++
	return out
}

// bufferedCap: the values the Subtractor keeps between calls (tests: it
// must not grow with the stream).
func (s *Subtractor) bufferedCap() int {
	c := cap(s.bufVo) + cap(s.bufOr) + cap(s.acc) + 2*cap(s.z) + cap(s.ring) + cap(s.bufOr2) + 2*cap(s.z2)
	for _, f := range s.ring {
		c += 2*cap(f.x) + 2*cap(f.y) + cap(f.num) + cap(f.den) + 2*cap(f.y2) + cap(f.num2) + cap(f.den2) + cap(f.y12)
	}
	return c
}
