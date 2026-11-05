package segments

import (
	"math"
	"math/bits"
	"sort"
)

// ItemSec is the time one chromaprint item covers (ffmpeg's chromaprint
// muxer, the default algorithm: 4096/3 samples at 11025 Hz).
const ItemSec = 4096.0 / 3 / 11025

// The matching of two fingerprints (measured on 2 Broke Girls S05: the
// shared theme matched at ≤ 6 of 32 bits per item).
const (
	maxBitErr = 6    // differing bits for two items to match
	gapSec    = 3.5  // a run survives this long without a match
	minDense  = 0.4  // a run's matching items over its length
	minVaried = 0.25 // distinct values over a run's length (silence and hum are not shared audio)
)

func items(sec float64) int { return int(math.Round(sec / ItemSec)) }

func secs(n int) float64 { return float64(n) * ItemSec }

// run is audio shared by two fingerprints: it starts at item A of the
// first and B of the second and is N items long.
type run struct{ A, B, N int }

// sharedRuns finds, over every alignment of a and b, the runs of matching
// items (gaps up to gapSec) at least minN and at most maxN items long,
// dense and varied enough to be real shared audio.
func sharedRuns(a, b []uint32, minN, maxN int) []run {
	gap := items(gapSec)
	var out []run
	for d := -(len(b) - 1); d < len(a); d++ {
		i0 := max(d, 0)
		i1 := min(len(a), len(b)+d)
		start, last, hits := -1, -1, 0
		closeRun := func() {
			if start < 0 {
				return
			}
			n := last - start + 1
			if n >= minN && n <= maxN && float64(hits) >= minDense*float64(n) && varied(a[start:last+1]) {
				out = append(out, run{A: start, B: start - d, N: n})
			}
			start, hits = -1, 0
		}
		for i := i0; i < i1; i++ {
			if bits.OnesCount32(a[i]^b[i-d]) > maxBitErr {
				continue
			}
			if start >= 0 && i-last-1 > gap {
				closeRun()
			}
			if start < 0 {
				start = i
			}
			last = i
			hits++
		}
		closeRun()
	}
	return out
}

// varied: a run of near-constant items is silence or hum, not a theme.
func varied(v []uint32) bool {
	seen := make(map[uint32]struct{}, len(v))
	for _, x := range v {
		seen[x] = struct{}{}
	}
	return float64(len(seen)) >= minVaried*float64(len(v))
}

// longest is the longest of runs (false: none).
func longest(rs []run) (run, bool) {
	if len(rs) == 0 {
		return run{}, false
	}
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].N != rs[j].N {
			return rs[i].N > rs[j].N
		}
		return rs[i].A < rs[j].A
	})
	return rs[0], true
}

// swap makes a run of (a, b) a run of (b, a).
func (r run) swap() run { return run{A: r.B, B: r.A, N: r.N} }
