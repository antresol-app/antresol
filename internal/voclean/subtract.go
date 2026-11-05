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
	s.analysed++
}

// synthesise subtracts the fitted original from frame k, overlap-adds it
// and emits the hop of samples it completes.
func (s *Subtractor) synthesise(k int, out []float32) []float32 {
	n, hop := s.n, s.hop
	lo, hi := max(k-s.back, 0), min(k+s.ahead, s.analysed-1)
	fr := &s.ring[k%len(s.ring)]
	for b := range fr.num {
		var num, den float64
		for j := lo; j <= hi; j++ {
			o := &s.ring[j%len(s.ring)]
			num += o.num[b]
			den += o.den[b]
		}
		g := min(max(num/max(den, denFloor), 0), s.gainCap)
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
	c := cap(s.bufVo) + cap(s.bufOr) + cap(s.acc) + 2*cap(s.z) + cap(s.ring)
	for _, f := range s.ring {
		c += 2*cap(f.x) + 2*cap(f.y) + cap(f.num) + cap(f.den)
	}
	return c
}
