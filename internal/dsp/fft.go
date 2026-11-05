package dsp

import (
	"math"
	"math/bits"
)

// FFT is an in-place iterative radix-2 complex FFT of one power-of-two
// size, its twiddles and bit reversal precomputed (no dependency, no cgo).
type FFT struct {
	n   int
	rev []int
	tw  []complex128 // e^{-2πik/n}, k < n/2
}

// NewFFT prepares the FFT of size n (a power of two).
func NewFFT(n int) *FFT {
	if n < 2 || n&(n-1) != 0 {
		panic("dsp: fft size must be a power of two")
	}
	f := &FFT{n: n, rev: make([]int, n), tw: make([]complex128, n/2)}
	shift := bits.UintSize - bits.Len(uint(n-1))
	for i := range f.rev {
		f.rev[i] = int(bits.Reverse(uint(i)) >> shift)
	}
	for k := range f.tw {
		s, c := math.Sincos(-2 * math.Pi * float64(k) / float64(n))
		f.tw[k] = complex(c, s)
	}
	return f
}

// Transform replaces x (len n) by its DFT, or by the unnormalised inverse
// DFT (n times the inverse) when inverse.
func (f *FFT) Transform(x []complex128, inverse bool) {
	n := f.n
	for i, j := range f.rev {
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half, step := size/2, n/size
		for start := 0; start < n; start += size {
			for k := range half {
				w := f.tw[k*step]
				if inverse {
					w = complex(real(w), -imag(w))
				}
				a, b := x[start+k], x[start+k+half]*w
				x[start+k], x[start+k+half] = a+b, a-b
			}
		}
	}
}
