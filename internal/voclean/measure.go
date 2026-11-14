package voclean

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"time"

	"github.com/antresol-app/antresol/internal/dsp"
	"github.com/antresol-app/antresol/internal/media"
)

const (
	// maxLagMs: a voice-over further off than this is of another cut.
	maxLagMs = 50
	// lagSearchMs: how far the lag is searched (so a track of another
	// cut is reported with its offset, not as noise).
	lagSearchMs = 3000
	// lagSpreadMs: the windows' lags may differ by this much (more: the
	// track drifts — another frame rate or cut).
	lagSpreadMs = 3
	// the fronts are the original scaled (residual/vo below frontMax) and
	// the centre carries the voices (above centreMin).
	frontMax  = 0.35
	centreMin = 0.5
	// decimate: the coarse lag search runs at rate/decimate.
	decimate = 12
	// the original's dialogue must be in the voice-over's centre: in the
	// original's dialogue blocks, the speech band's gain and coherence
	// (energy-weighted) of the voice-over's centre against the original's
	minDialogueGain      = 0.1
	minDialogueCoherence = 0.5
	minDialogueFrames    = 64
	// the original's dialogue must come through in at least
	// minDialogueWindows of the windows separately (their blocks' upper
	// quartile ≥ minDialogueCoherence), and the mean over all blocks be
	// ≥ minDialogueMean: one window of the original in a dub is not enough
	minDialogueWindows = 2
	minDialogueMean    = 0.35
	// minRefineCorr: the refined lag is used only where the start
	// correlates this well (normalised); else the windows' lag stays
	minRefineCorr = 0.5
	// dialogueQuantile: the coherence judged is the upper quartile of the
	// dialogue blocks' (the mean is pulled down by every block where the
	// Russian voice covers the English in the same bins — a voice-over's
	// normal state; a real voice-over clip's mean is 0.50)
	dialogueQuantile = 0.75
	// the lag Clean uses is refined on the file's first refineMs read from
	// its start, within ±refineSpan samples (3 ms) of the windows' lag
	refineMs   = 30_000
	refineSpan = 3 * rate / 1000
)

var measureAt = [3]float64{0.15, 0.45, 0.75}

// measureWindowMs: each of the three measured windows (at measureAt of
// the duration); all must agree. (A variable: tests shorten it.)
var measureWindowMs int64 = 60_000

// Pair is a voice-over track and the original track it is measured (and
// cleaned) against. VoInput: 0 when the voice-over is a stream of the
// file, k when it is the external track Input.External[k-1] (its Vo.Index
// is then ExternalIndex of its path).
type Pair struct {
	Vo, Orig media.AudioStream
	VoInput  int
}

// ExternalTrack is a voice-over beside the video (stage B: a file of its
// own) and the offset of its fit (vofit; ms, positive: later).
type ExternalTrack struct {
	Path     string
	OffsetMs float64
}

// ExternalIndex is the vo_index of an external voice-over in
// cleaned_audio: negative (no stream of a file has it), stable for its
// path (the fnv32a of it).
func ExternalIndex(path string) int {
	h := fnv.New32a()
	h.Write([]byte(path))
	return -1 - int(h.Sum32()&0x3fffffff)
}

// Input is the file the tracks are read from.
type Input struct {
	Path       string
	DurationMs int64
	// Busy (nil: never): ffmpeg is held stopped while it says playback
	// needs the disk.
	Busy func() bool
	// Limit: ffmpeg is killed once it has run (not been held) this long;
	// 0: no limit.
	Limit time.Duration
	// External: the voice-overs beside the video the pairs take from
	// (Pair.VoInput k is External[k-1]).
	External []ExternalTrack
}

// Measure is what Eligible found: the voice-over's lag behind the
// original (ms, positive: it runs late, both on the file's timeline), the
// residual of each front channel (FL, FR) and of the centre after the best
// single gain of the original's same channel, relative to the voice-over's
// level — the worst of the three windows; the speech-band gain and
// coherence of the voice-over's centre against the original's over the
// original's dialogue (DialogueFrames STFT frames); and the verdict (Why:
// the reason when not eligible).
type Measure struct {
	LagMs             float64    `json:"lagMs"`
	FrontResidual     [2]float64 `json:"frontResidual"`
	CentreResidual    float64    `json:"centreResidual"`
	DialogueGain      float64    `json:"dialogueGain"`
	DialogueCoherence float64    `json:"dialogueCoherence"`
	// DialogueCoherenceMean: the weighted mean over all dialogue blocks
	// (kept for the record; the verdict uses DialogueCoherence, the upper
	// quartile of the blocks)
	DialogueCoherenceMean float64 `json:"dialogueCoherenceMean"`
	// DialogueWindows: the windows whose dialogue blocks' upper quartile
	// passed on their own
	DialogueWindows int `json:"dialogueWindows"`
	// RefineCorr: the normalised correlation of the refine read (the
	// file's start) at the refined lag; below minRefineCorr the windows'
	// lag was kept
	RefineCorr float64 `json:"refineCorr"`
	// FrontGain: the voice-over's fronts over the original's (the mean of
	// FL and FR, over the measured windows) — the level the original is
	// mixed at, which bounds the centre's gain (GainCap)
	FrontGain      float64 `json:"frontGain"`
	DialogueFrames int     `json:"dialogueFrames"`
	Eligible       bool    `json:"eligible"`
	Why            string  `json:"why,omitempty"`
}

// window is one measured window of a pair.
type window struct {
	lag      int        // samples
	residual [3]float64 // FL FR FC
	gain     float64    // the fronts' mean least-squares gain
	measured bool       // residual is measured (not: silent, or another cut)
	dlg      dialogue
	why      string
}

// hasCentre: the layouts cleaned by subtraction — 5.1 and 7.1, whose
// centre carries the voices. Stereo waits for the ML step (subtracting
// both channels takes the music and effects with the English).
func hasCentre(ch int) bool { return ch == 6 || ch == 8 }

// stereoOver: a stereo voice-over over a 5.1 or 7.1 original — its left
// and right hold the original's centre (the dialogue) mixed down, cleaned
// by subtracting that centre from both (NewSubtractor2, the fronts fitted
// alongside); measured on its mid (L+R) and side (L−R) against the
// original's stereo downmix.
func stereoOver(p Pair) bool { return p.Vo.Channels == 2 && hasCentre(p.Orig.Channels) }

// layoutWhy says why a pair cannot be cleaned by its layouts ("" if it
// can).
func layoutWhy(p Pair) string {
	switch {
	case stereoOver(p):
		return ""
	case p.Vo.Channels != p.Orig.Channels:
		return fmt.Sprintf("layouts differ: %d and %d channels", p.Vo.Channels, p.Orig.Channels)
	case p.Vo.Channels == 2:
		return "stereo: not cleaned by subtraction (waits for the ML step)"
	case !hasCentre(p.Vo.Channels):
		return fmt.Sprintf("%d channels: only 5.1 and 7.1", p.Vo.Channels)
	}
	return ""
}

// Eligible decides for each pair whether its voice-over is laid over its
// original (same master, aligned within maxLagMs, the original's dialogue
// still in the centre) so that subtracting the original leaves only the
// voices: measured on three windows of the file, each read once for all
// the pairs. An error is a failure to read (ffmpeg); a track that does not
// qualify is a Measure with Eligible false and Why.
func Eligible(ctx context.Context, ffmpeg string, in Input, pairs []Pair) ([]Measure, error) {
	ms := make([]Measure, len(pairs))
	type state struct {
		active  bool
		windows []window
	}
	st := make([]state, len(pairs))
	for i, p := range pairs {
		switch why := layoutWhy(p); {
		case why != "":
			ms[i].Why = why
		case in.DurationMs < 5000:
			ms[i].Why = "too short to measure"
		default:
			st[i].active = true
		}
	}
	winMs := min(int64(measureWindowMs), in.DurationMs)
	for _, at := range measureAt {
		var act []int
		for i := range pairs {
			if st[i].active {
				act = append(act, i)
			}
		}
		if len(act) == 0 {
			break
		}
		start := max(min(int64(at*float64(in.DurationMs)), in.DurationMs-winMs), 0)
		ws, dl, err := measureWindow(ctx, ffmpeg, in, pairs, act, start, winMs)
		if err != nil {
			return nil, err
		}
		for k, i := range act {
			w := ws[k]
			if w.why != "" {
				w.why = fmt.Sprintf("at %d%%: %s", int(at*100), w.why)
				st[i].active = false
			}
			w.dlg = dl[k]
			st[i].windows = append(st[i].windows, w)
		}
	}
	var ok []int
	for i, p := range pairs {
		if len(st[i].windows) > 0 {
			ms[i] = combine(p.Vo.Channels, st[i].windows)
		}
		if ms[i].Eligible {
			ok = append(ok, i)
		}
	}
	if len(ok) > 0 {
		if err := refineLags(ctx, ffmpeg, in, pairs, ok, ms); err != nil {
			return nil, err
		}
	}
	return ms, nil
}

// refineLags sets the lag of the eligible pairs ok to the sample on the
// timeline Clean reads (the file from its start): the windows are read
// after an input seek, which trims each stream at its own packet
// timestamps — Matroska's are rounded to the millisecond, so the windows'
// lag may be off by a fraction of a millisecond, already too much to
// subtract speech. One read of the file's first refineMs for all of them;
// the windows' lag stays where the start is silent.
func refineLags(ctx context.Context, ffmpeg string, in Input, pairs []Pair, ok []int, ms []Measure) error {
	srcs, pos := streamsOf(pairs, ok)
	// the fronts only: the original scaled, where the centre carries the
	// Russian voice too (the normalised correlation is the test)
	mixes, err := downmixes(ctx, ffmpeg, in, srcs, 0, min(int64(refineMs), in.DurationMs), 2)
	if err != nil {
		return err
	}
	for k, i := range ok {
		a, b := mixes[pos[k][0]], mixes[pos[k][1]]
		if energy(a) == 0 || energy(b) == 0 {
			continue
		}
		lag, corr := refineLag(a, b, int(math.Round(ms[i].LagMs*rate/1000)))
		ms[i].LagMs, ms[i].RefineCorr = float64(lag)*1000/rate, corr
	}
	return nil
}

// refineLag searches a against b within ±refineSpan samples of lag c:
// the best lag and its normalised correlation; c itself when that is
// below minRefineCorr (the start does not tell).
func refineLag(a, b []float32, c int) (int, float64) {
	best, lag := math.Inf(-1), c
	for l := c - refineSpan; l <= c+refineSpan; l++ {
		var s float64
		for t := max(l, 0); t < len(a) && t-l < len(b); t++ {
			s += float64(a[t]) * float64(b[t-l])
		}
		if s > best {
			best, lag = s, l
		}
	}
	var aa, bb float64
	for t := max(lag, 0); t < len(a) && t-lag < len(b); t++ {
		aa += float64(a[t]) * float64(a[t])
		bb += float64(b[t-lag]) * float64(b[t-lag])
	}
	if aa == 0 || bb == 0 {
		return c, 0
	}
	corr := best / math.Sqrt(aa*bb)
	if corr < minRefineCorr {
		return c, corr
	}
	return lag, corr
}

// combine makes the Measure of a pair from its windows (the last one
// failed, or all three passed).
func combine(ch int, ws []window) Measure {
	var m Measure
	var dlg dialogue
	for _, w := range ws {
		dlg.add(w.dlg)
		if w.dlg.frames > 0 && w.dlg.quantile(dialogueQuantile) >= minDialogueCoherence {
			m.DialogueWindows++
		}
	}
	m.CentreResidual = math.Inf(1)
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, w := range ws {
		lagMs := float64(w.lag) * 1000 / rate
		lo, hi = min(lo, lagMs), max(hi, lagMs)
		m.FrontResidual[0] = max(m.FrontResidual[0], w.residual[0])
		m.FrontResidual[1] = max(m.FrontResidual[1], w.residual[1])
		if w.measured {
			m.CentreResidual = min(m.CentreResidual, w.residual[2])
		}
		if w.why != "" && m.Why == "" {
			m.Why = w.why
			m.LagMs = lagMs
		}
	}
	if math.IsInf(m.CentreResidual, 1) {
		m.CentreResidual = 0
	}
	var gn int
	for _, w := range ws {
		if w.measured {
			m.FrontGain += w.gain
			gn++
		}
	}
	if gn > 0 {
		m.FrontGain /= float64(gn)
	}
	m.DialogueFrames = dlg.frames
	if dlg.gainDen > 0 {
		m.DialogueGain = dlg.gainNum / dlg.gainDen
	}
	if dlg.cohDen > 0 {
		m.DialogueCoherenceMean = dlg.cohNum / dlg.cohDen
		m.DialogueCoherence = dlg.quantile(dialogueQuantile)
	}
	if m.Why != "" {
		return m
	}
	m.LagMs = (lo + hi) / 2
	switch {
	case hi-lo > lagSpreadMs:
		m.Why = fmt.Sprintf("lag varies %.1f…%.1f ms: drifts (another cut or frame rate)", lo, hi)
	case m.DialogueFrames < minDialogueFrames:
		m.Why = "no dialogue of the original found to check the centre against"
	case m.DialogueGain < minDialogueGain || m.DialogueWindows < minDialogueWindows || m.DialogueCoherenceMean < minDialogueMean:
		m.Why = fmt.Sprintf("the original's dialogue is not in the centre (gain %.2f, coherence in %d of %d windows, mean %.2f): a dub, not a voice-over",
			m.DialogueGain, m.DialogueWindows, len(ws), m.DialogueCoherenceMean)
	default:
		m.Eligible = true
	}
	return m
}

// streamsOf lists the distinct streams of the pairs act: the sources to
// decode and each pair's (vo, orig) position among them.
func streamsOf(pairs []Pair, act []int) ([]source, [][2]int) {
	var srcs []source
	at := map[[2]int]int{}
	pos := make([][2]int, len(act))
	add := func(s media.AudioStream, input int) int {
		key := [2]int{input, s.Index}
		if k, ok := at[key]; ok {
			return k
		}
		at[key] = len(srcs)
		srcs = append(srcs, source{input: input, idx: s.Index, ch: s.Channels})
		return len(srcs) - 1
	}
	for k, i := range act {
		pos[k] = [2]int{add(pairs[i].Vo, pairs[i].VoInput), add(pairs[i].Orig, 0)}
	}
	return srcs, pos
}

// measureWindow measures startMs…+durMs for the pairs act: the lag from
// the front downmix (one read of the window), then each pair's residuals
// and dialogue at its lag (a second read).
func measureWindow(ctx context.Context, ffmpeg string, in Input, pairs []Pair, act []int, startMs, durMs int64) ([]window, []dialogue, error) {
	ws := make([]window, len(act))
	dls := make([]dialogue, len(act))
	srcs, pos := streamsOf(pairs, act)
	mixes, err := downmixes(ctx, ffmpeg, in, srcs, startMs, durMs, 3)
	if err != nil {
		return nil, nil, err
	}
	var next []int // positions in act going on to the second read
	for k := range act {
		a, b := mixes[pos[k][0]], mixes[pos[k][1]]
		if energy(a) == 0 || energy(b) == 0 {
			ws[k].why = "silent"
			continue
		}
		ws[k].lag = findLag(a, b, lagSearchMs*rate/1000)
		if lagMs := math.Abs(float64(ws[k].lag)) * 1000 / rate; lagMs > maxLagMs {
			ws[k].why = fmt.Sprintf("lag %.0f ms beyond ±%d ms (another cut)", float64(ws[k].lag)*1000/rate, maxLagMs)
			continue
		}
		next = append(next, k)
	}
	mixes = nil
	if len(next) == 0 {
		return ws, dls, nil
	}
	act2 := make([]int, len(next))
	lags := make([]int, len(next))
	for j, k := range next {
		act2[j], lags[j] = act[k], ws[k].lag
	}
	res, gains, dl, err := residuals(ctx, ffmpeg, in, pairs, act2, lags, startMs, durMs)
	if err != nil {
		return nil, nil, err
	}
	for j, k := range next {
		r := res[j]
		ws[k].residual, ws[k].gain, ws[k].measured = r, gains[j], true
		dls[k] = dl[j]
		switch {
		case r[0] >= frontMax || r[1] >= frontMax:
			ws[k].why = fmt.Sprintf("front residual %.2f/%.2f ≥ %.2f: not laid over the original", r[0], r[1], frontMax)
		case r[2] <= centreMin:
			ws[k].why = fmt.Sprintf("centre residual %.2f ≤ %.2f: no voices over the original", r[2], centreMin)
		}
	}
	return ws, dls, nil
}

// downmixes decodes the window of the sources (one read) and returns the
// sum of each one's first front channels (FL FR, or FL FR FC: one 60 s
// mono buffer per stream: bounded).
func downmixes(ctx context.Context, ffmpeg string, in Input, srcs []source, startMs, durMs int64, front int) ([][]float32, error) {
	d, err := startDecoder(ctx, ffmpeg, in.Path, in.External, srcs, startMs, durMs, in.Busy, in.Limit)
	if err != nil {
		return nil, err
	}
	out := make([][]float32, len(srcs))
	bufs := make([][]float32, len(srcs))
	for k, s := range srcs {
		out[k] = make([]float32, 0, int(durMs)*rate/1000+rate)
		bufs[k] = make([]float32, readFrames*s.ch)
	}
	for {
		any := false
		for k, s := range srcs {
			n, err := d.read(k, bufs[k])
			if err != nil {
				d.abort()
				return nil, err
			}
			any = any || n > 0
			for i := range n {
				var v float32
				for c := range min(front, s.ch) {
					v += bufs[k][i*s.ch+c]
				}
				out[k] = append(out[k], v)
			}
		}
		if !any {
			break
		}
	}
	return out, d.wait()
}

func energy(x []float32) float64 {
	var e float64
	for _, v := range x {
		e += float64(v) * float64(v)
	}
	return e
}

// findLag returns the lag L (samples, |L| ≤ maxLag) maximising Σ a[t]·b[t−L]:
// a coarse search on block means of decimate samples (FFT cross-
// correlation), refined at the full rate around it.
func findLag(a, b []float32, maxLag int) int {
	da, db := decimated(a), decimated(b)
	p := 1
	for p < len(da)+len(db) {
		p <<= 1
	}
	za, zb := make([]complex128, p), make([]complex128, p)
	for i, v := range da {
		za[i] = complex(v, 0)
	}
	for i, v := range db {
		zb[i] = complex(v, 0)
	}
	f := dsp.NewFFT(p)
	f.Transform(za, false)
	f.Transform(zb, false)
	for i := range za {
		v := zb[i]
		za[i] *= complex(real(v), -imag(v))
	}
	f.Transform(za, true) // za[k] = Σ da[t+k]·db[t] (circular)
	k := maxLag / decimate
	best, bestK := math.Inf(-1), 0
	for l := -k; l <= k; l++ {
		if v := real(za[(l+p)%p]); v > best {
			best, bestK = v, l
		}
	}
	za, zb = nil, nil
	// refine at the full rate
	best, lag := math.Inf(-1), bestK*decimate
	for l := bestK*decimate - 2*decimate; l <= bestK*decimate+2*decimate; l++ {
		if l < -maxLag || l > maxLag {
			continue
		}
		var s float64
		for t := max(l, 0); t < len(a) && t-l < len(b); t++ {
			s += float64(a[t]) * float64(b[t-l])
		}
		if s > best {
			best, lag = s, l
		}
	}
	return lag
}

func decimated(x []float32) []float64 {
	out := make([]float64, len(x)/decimate)
	for i := range out {
		var s float64
		for _, v := range x[i*decimate : (i+1)*decimate] {
			s += float64(v)
		}
		out[i] = s / decimate
	}
	return out
}

// aligner pairs a voice-over's frames with its original's lag frames
// earlier: vo[t] with orig[t−lag], the head of the one ahead dropped.
// Its queues hold at most |lag| frames plus a block.
type aligner struct {
	vch, och       int
	vo, or         []float32 // interleaved queues
	dropVo, dropOr int
}

func newAligner(vch, och, lag int) *aligner {
	a := &aligner{vch: vch, och: och}
	if lag > 0 {
		a.dropVo = lag
	} else {
		a.dropOr = -lag
	}
	return a
}

// push adds blocks of both and returns the aligned frames available (the
// caller uses them before the next push).
func (a *aligner) push(vb, ob []float32) (vo, or []float32, n int) {
	vb, a.dropVo = drop(vb, a.dropVo, a.vch)
	ob, a.dropOr = drop(ob, a.dropOr, a.och)
	a.vo = append(a.vo[:len(a.vo):len(a.vo)], vb...)
	a.or = append(a.or[:len(a.or):len(a.or)], ob...)
	n = min(len(a.vo)/a.vch, len(a.or)/a.och)
	vo, or = a.vo[:n*a.vch], a.or[:n*a.och]
	a.vo, a.or = a.vo[n*a.vch:], a.or[n*a.och:]
	return vo, or, n
}

func drop(b []float32, frames, ch int) ([]float32, int) {
	k := min(frames, len(b)/ch)
	return b[k*ch:], frames - k
}

// downmixLR: the stereo downmix of a 5.1 or 7.1 frame (FL FR FC LFE BL
// BR [SL SR]): each front with the centre and its surrounds at −3 dB, the
// LFE left out (ffmpeg's and the usual broadcast downmix).
func downmixLR(f []float32) (l, r float64) {
	const h = 0.7071
	l = float64(f[0]) + h*float64(f[2]) + h*float64(f[4])
	r = float64(f[1]) + h*float64(f[2]) + h*float64(f[5])
	if len(f) >= 8 {
		l += h * float64(f[6])
		r += h * float64(f[7])
	}
	return l, r
}

// residuals streams the window once for the pairs act at their lags:
// per pair and FL FR FC, rms(x − g·y)/rms(x) with g the least-squares
// gain of the original's channel y in the voice-over's x; and the
// dialogue sums of the centres.
func residuals(ctx context.Context, ffmpeg string, in Input, pairs []Pair, act, lags []int, startMs, durMs int64) ([][3]float64, []float64, []dialogue, error) {
	srcs, pos := streamsOf(pairs, act)
	d, err := startDecoder(ctx, ffmpeg, in.Path, in.External, srcs, startMs, durMs, in.Busy, in.Limit)
	if err != nil {
		return nil, nil, nil, err
	}
	type acc struct {
		al         *aligner
		xy, yy, xx [3]float64
		dlg        *dlgAcc
	}
	accs := make([]*acc, len(act))
	for k, i := range act {
		accs[k] = &acc{al: newAligner(pairs[i].Vo.Channels, pairs[i].Orig.Channels, lags[k]), dlg: newDlgAcc()}
	}
	bufs := make([][]float32, len(srcs))
	ns := make([]int, len(srcs))
	for k, s := range srcs {
		bufs[k] = make([]float32, readFrames*s.ch)
	}
	for {
		any := false
		for k := range srcs {
			n, err := d.read(k, bufs[k])
			if err != nil {
				d.abort()
				return nil, nil, nil, err
			}
			ns[k] = n
			any = any || n > 0
		}
		if !any {
			break
		}
		for k, a := range accs {
			v, o := pos[k][0], pos[k][1]
			vo, or, n := a.al.push(bufs[v][:ns[v]*srcs[v].ch], bufs[o][:ns[o]*srcs[o].ch])
			vc, oc := srcs[v].ch, srcs[o].ch
			stereo := stereoOver(pairs[act[k]])
			for i := range n {
				vf, of := vo[i*vc:(i+1)*vc], or[i*oc:(i+1)*oc]
				var x, y [3]float64
				voCentre := vf[min(2, vc-1)]
				if stereo {
					// side (twice: the fronts' check) and mid of the voice-over
					// against the original's stereo downmix
					l, r := downmixLR(of)
					x = [3]float64{float64(vf[0] - vf[1]), float64(vf[0] - vf[1]), float64(vf[0] + vf[1])}
					y = [3]float64{l - r, l - r, l + r}
					voCentre = (vf[0] + vf[1]) / 2
				} else {
					for c := range 3 {
						x[c], y[c] = float64(vf[c]), float64(of[c])
					}
				}
				for c := range 3 {
					a.xy[c] += x[c] * y[c]
					a.yy[c] += y[c] * y[c]
					a.xx[c] += x[c] * x[c]
				}
				a.dlg.push(voCentre, of[2], of[0], of[1])
			}
		}
	}
	if err := d.wait(); err != nil {
		return nil, nil, nil, err
	}
	res := make([][3]float64, len(act))
	gains := make([]float64, len(act))
	dls := make([]dialogue, len(act))
	for k, a := range accs {
		for c := range 3 {
			if a.xx[c] == 0 { // a silent channel of the voice-over: not a fit
				res[k][c] = 1
				continue
			}
			r := a.xx[c]
			if a.yy[c] > 0 {
				r -= a.xy[c] * a.xy[c] / a.yy[c]
			}
			res[k][c] = math.Sqrt(max(r, 0) / a.xx[c])
		}
		var nc int
		for c := range 2 {
			if a.yy[c] > 0 {
				gains[k] += a.xy[c] / a.yy[c]
				nc++
			}
		}
		if nc > 0 {
			gains[k] /= float64(nc)
		}
		dls[k] = a.dlg.out
	}
	return res, gains, dls, nil
}

const (
	// the dialogue check's STFT and speech band
	dlgN, dlgHop  = 2048, 1024
	dlgBlock      = 16 // frames per coherence estimate (≈ 0.34 s)
	dlgLo, dlgHi  = 300, 3400
	dlgFloorShare = 1e-4 // per-bin amplitude below this (re full scale): silence
)

// dialogue: sums of the dialogue check over the original's dialogue
// blocks: per bin the coherence and the least-squares gain of the
// original's centre in the voice-over's, weighted by the original
// centre's energy above its fronts' (its dialogue, not the music and
// effects all channels share).
type dialogue struct {
	frames                           int
	cohNum, cohDen, gainNum, gainDen float64
	// hist: the dialogue blocks by their coherence (cohBins buckets of
	// [0, 1]): a voice-over lets the original's voice through clearly in
	// part of them (between and under its lines), a dub in none
	hist [cohBins]int
}

// cohBins: the resolution of the block coherence histogram.
const cohBins = 50

func (d *dialogue) add(o dialogue) {
	d.frames += o.frames
	d.cohNum += o.cohNum
	d.cohDen += o.cohDen
	d.gainNum += o.gainNum
	d.gainDen += o.gainDen
	for i, n := range o.hist {
		d.hist[i] += n
	}
}

// quantile is the block coherence below which the share q of the dialogue
// blocks lie (0 without blocks).
func (d *dialogue) quantile(q float64) float64 {
	total := 0
	for _, n := range d.hist {
		total += n
	}
	if total == 0 {
		return 0
	}
	seen := 0
	for i, n := range d.hist {
		seen += n
		if float64(seen) >= q*float64(total) {
			return (float64(i) + 0.5) / cohBins
		}
	}
	return 1
}

// dlgAcc finds the original's dialogue blocks — its centre's speech band
// louder than its two fronts' together — and measures there how much of
// the original's dialogue the voice-over's centre holds: per bin the
// magnitude-squared coherence over the block (high where the original's
// voice is mixed in, whatever else plays in other bins) and the gain,
// weighted by the original centre's energy above the fronts' average — a
// dub made on the original's M&E shares the music and effects, coherent
// too, but not the voice.
type dlgAcc struct {
	f             *dsp.FFT
	win           []float64
	buf           [4][]float64 // vo centre, orig centre, orig FL, orig FR
	fill          int
	z1, z2        []complex128
	lo, hi        int
	sxy           []complex128
	sxx, syy, sff []float64
	ec, ef        float64
	nf            int
	out           dialogue
}

func newDlgAcc() *dlgAcc {
	a := &dlgAcc{f: dsp.NewFFT(dlgN), win: make([]float64, dlgN), z1: make([]complex128, dlgN), z2: make([]complex128, dlgN),
		lo: int(math.Ceil(dlgLo * dlgN / rate)), hi: int(dlgHi * dlgN / rate)}
	for i := range a.win {
		a.win[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/dlgN)
	}
	for i := range a.buf {
		a.buf[i] = make([]float64, dlgN)
	}
	nb := a.hi - a.lo + 1
	a.sxy, a.sxx, a.syy, a.sff = make([]complex128, nb), make([]float64, nb), make([]float64, nb), make([]float64, nb)
	return a
}

func (a *dlgAcc) push(vc, oc, ol, or float32) {
	a.buf[0][a.fill], a.buf[1][a.fill], a.buf[2][a.fill], a.buf[3][a.fill] = float64(vc), float64(oc), float64(ol), float64(or)
	a.fill++
	if a.fill < dlgN {
		return
	}
	a.frame()
	for i := range a.buf {
		copy(a.buf[i], a.buf[i][dlgHop:])
	}
	a.fill = dlgN - dlgHop
}

// unpack splits the spectrum z of p + i·q at bin k into P and Q.
func unpack(z []complex128, k int) (complex128, complex128) {
	a, b := z[k], z[(len(z)-k)%len(z)]
	bc := complex(real(b), -imag(b))
	d := (a - bc) / 2
	return (a + bc) / 2, complex(imag(d), -real(d))
}

func abs2(c complex128) float64 { return real(c)*real(c) + imag(c)*imag(c) }

func (a *dlgAcc) frame() {
	for i := range dlgN {
		w := a.win[i]
		a.z1[i] = complex(w*a.buf[0][i], w*a.buf[1][i])
		a.z2[i] = complex(w*a.buf[2][i], w*a.buf[3][i])
	}
	a.f.Transform(a.z1, false)
	a.f.Transform(a.z2, false)
	for k := a.lo; k <= a.hi; k++ {
		x, y := unpack(a.z1, k)
		l, r := unpack(a.z2, k)
		j := k - a.lo
		a.sxy[j] += x * complex(real(y), -imag(y))
		a.sxx[j] += abs2(x)
		a.syy[j] += abs2(y)
		a.sff[j] += (abs2(l) + abs2(r)) / 2
		a.ec += abs2(y)
		a.ef += abs2(l) + abs2(r)
	}
	a.nf++
	if a.nf == dlgBlock {
		a.block()
	}
}

func (a *dlgAcc) block() {
	bins := float64(len(a.syy))
	floor := math.Pow(dlgFloorShare*dlgN/4, 2)
	if a.ec > a.ef && a.ec/(float64(a.nf)*bins) > floor {
		a.out.frames += a.nf
		var bn, bd float64
		for j := range a.syy {
			w := a.syy[j] - a.sff[j] // the dialogue's share of the bin
			if w <= 0 || a.sxx[j] <= 0 {
				continue
			}
			msc := abs2(a.sxy[j]) / (a.sxx[j] * a.syy[j])
			bn += w * msc
			bd += w
			a.out.gainNum += w * real(a.sxy[j]) / a.syy[j]
			a.out.gainDen += w
		}
		a.out.cohNum += bn
		a.out.cohDen += bd
		if bd > 0 {
			a.out.hist[min(int(bn/bd*cohBins), cohBins-1)]++
		}
	}
	clear(a.sxy)
	clear(a.sxx)
	clear(a.syy)
	clear(a.sff)
	a.ec, a.ef, a.nf = 0, 0, 0
}
