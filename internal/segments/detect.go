package segments

import (
	"math"
	"sort"
)

// AnalysisVersion is the version of the analysis below; files analysed by
// an older one are analysed again (3: a recap may end in the black run
// the intro starts in; 4: a season whose chapter recaps run into the
// titles lends that to its episodes without chapters).
const AnalysisVersion = 4

// The analysis' limits (from the skip-segments design).
const (
	introMinSec = 15.0
	// shortIntroSec: a shorter title card (2 Broke Girls: 11 s) counts when
	// it does not start in the first shortIntroLeadSec (a run at 0:00 is a
	// studio or channel sting: After Life's 9 s) and two neighbours agree.
	shortIntroSec     = 8.0
	shortIntroLeadSec = 3.0
	introMaxSec       = 120.0
	headMaxSec        = 600.0 // the intro is looked for in the first min(25 %, 10 min)
	headShare         = 0.25
	tailSec           = 450.0 // the credits in the last 450 s of an episode
	movieTailSec      = 900.0 // and of a film (their credits run longer)
	creditsMinSec     = 10.0
	// creditsAgreeSec: neighbours agree on a credits start this close
	creditsAgreeSec = 15.0
	// postCreditsSec: credits ending this long before the end of the file
	// leave a scene after them.
	postCreditsSec = 20.0
	// agreeSec: two partners confirm the same intro when its start and
	// end agree this closely.
	agreeSec = 3.0
	// partners: each episode is compared with this many neighbours on
	// each side (episode order), not with the whole season.
	partners = 2
	// black frames: a credits start moves back to a black run this close
	// before it; credits from black frames alone need this much of it.
	blackSnapSec       = 10.0
	blackCreditsMinSec = 30.0
	blackJoinSec       = 2.0
	// the recap: a sting of shared audio in the first stingWithinSec, then
	// recap clips up to a black run of at least recapBlackSec.
	stingWithinSec = 120.0
	stingMinSec    = 1.5
	stingMaxSec    = 8.0
	recapMinSec    = 15.0
	recapMaxSec    = 120.0
	recapBlackSec  = 2.0
	// recapClipSec: a recap holds audio of an earlier episode at least this long
	recapClipSec = 3.0
	// meetSec: a chapter recap runs into the titles when the intro's music
	// starts at most this long after it ends (the fade to black between).
	meetSec = 6.0
)

// HeadSec is how much of the start of a file of durSec the intro is looked for in.
func HeadSec(durSec float64) float64 { return math.Min(durSec*headShare, headMaxSec) }

// TailSec is how much of the end of a file the credits are looked for in.
func TailSec(durSec float64, movie bool) float64 {
	t := tailSec
	if movie {
		t = movieTailSec
	}
	return math.Min(t, durSec/2)
}

// Span is a stretch of a file in seconds.
type Span struct{ Start, End float64 }

func (s Span) Len() float64 { return s.End - s.Start }

// Episode is what the analysis knows of one file of a season.
type Episode struct {
	File    int64 // files.id
	Item    int64 // the episode: two files of one episode are never compared
	Number  int   // episode number, for the neighbours
	DurSec  float64
	Head    []uint32 // fingerprint of [0, HeadSec)
	Tail    []uint32 // fingerprint of [TailStart, DurSec)
	TailAt  float64  // TailStart
	Black   []Span   // black runs within the tail window
	HeadErr bool     // the head could not be fingerprinted
}

// neighbours lists, for episode i, the other episodes (not of the same
// item) it is compared with: up to `partners` on each side in episode order.
func neighbours(eps []Episode, i int) []int {
	var out []int
	for dir := -1; dir <= 1; dir += 2 {
		n := 0
		for j := i + dir; j >= 0 && j < len(eps) && n < partners; j += dir {
			if eps[j].Item == eps[i].Item {
				continue
			}
			out = append(out, j)
			n++
		}
	}
	return out
}

// sortEpisodes puts a season in episode order.
func sortEpisodes(eps []Episode) {
	sort.SliceStable(eps, func(i, j int) bool {
		if eps[i].Number != eps[j].Number {
			return eps[i].Number < eps[j].Number
		}
		return eps[i].File < eps[j].File
	})
}

// pairCache keeps the runs of a pair of fingerprints (compared once for
// both episodes).
type pairCache map[[2]int][]run

func (c pairCache) runs(i, j int, fp func(int) []uint32, minN, maxN int) []run {
	if rs, ok := c[[2]int{i, j}]; ok {
		return rs
	}
	if rs, ok := c[[2]int{j, i}]; ok {
		out := make([]run, len(rs))
		for k, r := range rs {
			out[k] = r.swap()
		}
		return out
	}
	rs := sharedRuns(fp(i), fp(j), minN, maxN)
	c[[2]int{i, j}] = rs
	return rs
}

// FindIntros finds the intro of each episode of a season: the longest audio
// run (15 s – 2 min) it shares with a neighbour, the one most neighbours
// agree on. A run of 8–15 s counts when it starts after the first 3 s and
// two neighbours agree on it. Keyed by file id; an episode without one is
// absent.
func FindIntros(eps []Episode) map[int64]Span {
	sortEpisodes(eps)
	cache := pairCache{}
	head := func(i int) []uint32 { return eps[i].Head }
	out := map[int64]Span{}
	for i := range eps {
		if len(eps[i].Head) == 0 {
			continue
		}
		var cands []Span
		for _, j := range neighbours(eps, i) {
			if len(eps[j].Head) == 0 {
				continue
			}
			var ok []run
			for _, r := range cache.runs(i, j, head, items(shortIntroSec), items(introMaxSec)) {
				if r.N >= items(introMinSec) || (r.A >= items(shortIntroLeadSec) && r.B >= items(shortIntroLeadSec)) {
					ok = append(ok, r)
				}
			}
			if r, found := longest(ok); found {
				cands = append(cands, Span{secs(r.A), secs(r.A + r.N)})
			}
		}
		if s, votes := agreed(cands); votes > 0 && (s.Len() >= introMinSec-0.01 || votes >= 2) {
			out[eps[i].File] = s
		}
	}
	return out
}

// agreed picks the candidate most others agree with (start and end within
// agreeSec), the longest among equals, and how many agree (itself
// included; 0: no candidates).
func agreed(cands []Span) (Span, int) {
	best, bestVotes := Span{}, -1
	for _, c := range cands {
		votes := 0
		for _, o := range cands {
			if math.Abs(c.Start-o.Start) <= agreeSec && math.Abs(c.End-o.End) <= agreeSec {
				votes++
			}
		}
		if votes > bestVotes || (votes == bestVotes && c.Len() > best.Len()) {
			best, bestVotes = c, votes
		}
	}
	return best, max(bestVotes, 0)
}

// FindCredits finds the credits of each episode of a season from the end
// audio it shares with its neighbours. Per neighbour one run counts: the
// longest that reaches the end of the file (within postCreditsSec), else
// the latest (credits before a scene); an earlier recurring cue never
// stretches it. With three or more episodes two neighbours must agree
// (starts within creditsAgreeSec); the start is their median, moved back
// to a black run just before it, and never before the tail window or the
// middle of the file. The end is the file's when the shared audio runs
// within postCreditsSec of it. Episodes without agreed end audio get
// credits from black frames alone (BlackCredits).
func FindCredits(eps []Episode) map[int64]Span {
	sortEpisodes(eps)
	cache := pairCache{}
	tail := func(i int) []uint32 { return eps[i].Tail }
	need := 1
	if len(eps) >= 3 {
		need = 2
	}
	out := map[int64]Span{}
	for i, e := range eps {
		var cands []Span
		if len(e.Tail) > 0 {
			for _, j := range neighbours(eps, i) {
				if len(eps[j].Tail) == 0 {
					continue
				}
				if c, ok := creditsRun(cache.runs(i, j, tail, items(creditsMinSec), len(e.Tail)), e); ok {
					cands = append(cands, c)
				}
			}
		}
		s, ok := creditsAgreed(cands, need)
		if ok {
			floor := math.Max(e.TailAt, e.DurSec/2)
			if s.Start < floor {
				s.Start = floor
			}
			for _, b := range e.Black {
				if b.Start < s.Start && b.Start >= s.Start-blackSnapSec && b.Start >= floor {
					s.Start = b.Start
					break
				}
			}
			if e.DurSec-s.End < postCreditsSec {
				s.End = e.DurSec
			}
			ok = s.End-s.Start >= creditsMinSec
		}
		if !ok {
			if b, ok := BlackCredits(e.Black, e.TailAt, e.DurSec); ok {
				out[e.File] = b
			}
			continue
		}
		out[e.File] = s
	}
	return out
}

// creditsRun is the run of one neighbour pair that marks e's credits: the
// longest reaching the end of the file, else the one ending latest.
func creditsRun(rs []run, e Episode) (Span, bool) {
	var best Span
	found, toEnd := false, false
	for _, r := range rs {
		sp := Span{e.TailAt + secs(r.A), e.TailAt + secs(r.A+r.N)}
		reaches := e.DurSec-sp.End < postCreditsSec
		better := !found
		switch {
		case found && reaches != toEnd:
			better = reaches
		case found && reaches:
			better = sp.Len() > best.Len()
		case found:
			better = sp.End > best.End
		}
		if !better {
			continue
		}
		best, found, toEnd = sp, true, reaches
	}
	return best, found
}

// creditsAgreed is the credits the neighbours agree on: the largest group
// of candidates whose starts lie within creditsAgreeSec of each other, at
// least need of them; the median start and end of that group.
func creditsAgreed(cands []Span, need int) (Span, bool) {
	var group []Span
	for _, c := range cands {
		var g []Span
		for _, o := range cands {
			if math.Abs(c.Start-o.Start) <= creditsAgreeSec {
				g = append(g, o)
			}
		}
		if len(g) > len(group) {
			group = g
		}
	}
	if len(group) < need || len(group) == 0 {
		return Span{}, false
	}
	starts, ends := make([]float64, len(group)), make([]float64, len(group))
	for k, g := range group {
		starts[k], ends[k] = g.Start, g.End
	}
	return Span{median(starts), median(ends)}, true
}

func median(v []float64) float64 {
	sort.Float64s(v)
	n := len(v)
	if n%2 == 1 {
		return v[n/2]
	}
	return (v[n/2-1] + v[n/2]) / 2
}

// joinBlack merges black runs closer than blackJoinSec (rolling credits
// flicker out of «black» between lines).
func joinBlack(black []Span) []Span {
	bs := append([]Span(nil), black...)
	sort.Slice(bs, func(i, j int) bool { return bs[i].Start < bs[j].Start })
	var out []Span
	for _, b := range bs {
		if n := len(out); n > 0 && b.Start-out[n-1].End <= blackJoinSec {
			out[n-1].End = math.Max(out[n-1].End, b.End)
			continue
		}
		out = append(out, b)
	}
	return out
}

// BlackCredits is the credits of a file from black frames only (a film, a
// lone episode, an episode sharing no end audio), when unambiguous: the
// last black stretch of the tail window is at least blackCreditsMinSec long
// and runs to the end of the file (under postCreditsSec short of it).
func BlackCredits(black []Span, tailAt, durSec float64) (Span, bool) {
	js := joinBlack(black)
	if len(js) == 0 || durSec <= 0 {
		return Span{}, false
	}
	last := js[len(js)-1]
	if last.Len() < blackCreditsMinSec || durSec-last.End >= postCreditsSec || last.Start < tailAt {
		return Span{}, false
	}
	return Span{last.Start, durSec}, true
}

// FindStings finds the «previously on» sting of each episode whose intro is
// known: the earliest audio (stingMinSec – stingMaxSec) in its first
// stingWithinSec it shares with a neighbour, ending well before the intro
// (a recap is at least recapMinSec). Keyed by file id: the sting's end.
func FindStings(eps []Episode, intros map[int64]Span) map[int64]float64 {
	sortEpisodes(eps)
	within := items(stingWithinSec)
	cut := func(i int) []uint32 {
		h := eps[i].Head
		if len(h) > within {
			h = h[:within]
		}
		return h
	}
	cache := pairCache{}
	out := map[int64]float64{}
	for i, e := range eps {
		in, ok := intros[e.File]
		if !ok || len(e.Head) == 0 {
			continue
		}
		var best *Span
		for _, j := range neighbours(eps, i) {
			for _, r := range cache.runs(i, j, cut, items(stingMinSec), items(stingMaxSec)) {
				s := Span{secs(r.A), secs(r.A + r.N)}
				if s.End > in.Start-recapMinSec {
					continue
				}
				if best == nil || s.Start < best.Start {
					best = &s
				}
			}
		}
		if best != nil {
			out[e.File] = best.End
		}
	}
	return out
}

// RecapAfter is an episode's recap from its sting's end, its intro and the
// black runs between them: 0:00 to the end of the first black run of at
// least recapBlackSec after the sting, 15 s – 2 min. A black run the intro's
// music starts in (the titles fade in from it) ends the recap at the
// intro's start; one that starts after the intro is no recap's end. False:
// no recap (when in doubt, none).
func RecapAfter(stingEnd float64, intro Span, black []Span) (Span, bool) {
	bs := append([]Span(nil), black...)
	sort.Slice(bs, func(i, j int) bool { return bs[i].Start < bs[j].Start })
	for _, b := range bs {
		if b.Start < stingEnd || b.Len() < recapBlackSec {
			continue
		}
		r := Span{0, math.Min(b.End, intro.Start)}
		if b.Start > intro.Start+0.5 || r.Len() < recapMinSec || r.Len() > recapMaxSec {
			return Span{}, false
		}
		return r, true
	}
	return Span{}, false
}

// RecapsIntoTitles reports whether a season's chapter-marked recaps all run
// straight into the titles (at least two of them): its other episodes'
// recaps then end at their intro too. recaps and intros are keyed by file.
func RecapsIntoTitles(recaps, intros map[int64]Span) bool {
	n := 0
	for f, r := range recaps {
		in, ok := intros[f]
		if !ok {
			continue
		}
		if gap := in.Start - r.End; gap < -0.5 || gap > meetSec {
			return false
		}
		n++
	}
	return n >= 2
}

// RecapIntoTitles is an episode's recap in a season whose recaps run into
// the titles: 0:00 to the intro's start, when a black run of at least
// recapBlackSec after the sting ends within meetSec before the intro or
// holds its start (15 s – 2 min). Dark shots inside the recap do not end it.
func RecapIntoTitles(stingEnd float64, intro Span, black []Span) (Span, bool) {
	for _, b := range black {
		if b.Start < stingEnd || b.Len() < recapBlackSec || b.Start > intro.Start+0.5 || b.End < intro.Start-meetSec {
			continue
		}
		r := Span{0, intro.Start}
		if r.Len() < recapMinSec || r.Len() > recapMaxSec {
			return Span{}, false
		}
		return r, true
	}
	return Span{}, false
}

// EarlierClip reports whether the stretch [from, to) of a file's head holds
// audio (recapClipSec or more) of an earlier episode of the season, its
// head or tail: a recap is clips of what came before; a cold open is new.
func EarlierClip(eps []Episode, fileID int64, from, to float64) bool {
	var me *Episode
	for k := range eps {
		if eps[k].File == fileID {
			me = &eps[k]
		}
	}
	if me == nil || len(me.Head) == 0 {
		return false
	}
	a, b := max(items(from), 0), min(items(to), len(me.Head))
	if b-a < items(recapClipSec) {
		return false
	}
	part := me.Head[a:b]
	for _, o := range eps {
		if o.Number >= me.Number || o.Item == me.Item {
			continue
		}
		for _, fp := range [][]uint32{o.Head, o.Tail} {
			if len(fp) > 0 && len(sharedRuns(part, fp, items(recapClipSec), len(part))) > 0 {
				return true
			}
		}
	}
	return false
}
