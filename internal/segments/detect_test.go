package segments

import (
	"math"
	"math/rand"
	"testing"
)

// noise is a fingerprint of unrelated audio: random items.
func noise(r *rand.Rand, sec float64) []uint32 {
	out := make([]uint32, items(sec))
	for i := range out {
		out[i] = r.Uint32()
	}
	return out
}

// blur flips up to 3 bits of each item: the same audio, encoded again.
func blur(r *rand.Rand, v []uint32) []uint32 {
	out := make([]uint32, len(v))
	for i, x := range v {
		for k := r.Intn(4); k > 0; k-- {
			x ^= 1 << r.Intn(32)
		}
		out[i] = x
	}
	return out
}

// put copies theme into fp at sec.
func put(r *rand.Rand, fp []uint32, sec float64, theme []uint32) {
	copy(fp[items(sec):], blur(r, theme))
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestSharedRunsGaps(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	theme := noise(r, 40)
	a, b := noise(r, 200), noise(r, 200)
	put(r, a, 50, theme)
	put(r, b, 80, theme)
	// 2 s of a are damaged: still one run
	for i := items(60); i < items(62); i++ {
		a[i] = r.Uint32()
	}
	got, ok := longest(sharedRuns(a, b, items(15), items(120)))
	if !ok || !near(secs(got.A), 50, 0.3) || !near(secs(got.B), 80, 0.3) || !near(secs(got.N), 40, 0.3) {
		t.Fatalf("2 s gap: %+v %v (%.1f s at %.1f)", got, ok, secs(got.N), secs(got.A))
	}
	// 5 s damaged: split in two, each under 40 s
	for i := items(60); i < items(65); i++ {
		a[i] = r.Uint32()
	}
	got, _ = longest(sharedRuns(a, b, items(15), items(120)))
	if secs(got.N) > 30 {
		t.Fatalf("5 s gap joined: %.1f s", secs(got.N))
	}
}

func season(r *rand.Rand, introAt []float64, themeSec float64) []Episode {
	theme := noise(r, themeSec)
	var eps []Episode
	for i, at := range introAt {
		h := noise(r, 300)
		if at >= 0 {
			put(r, h, at, theme)
		}
		eps = append(eps, Episode{File: int64(100 + i), Item: int64(10 + i), Number: i + 1, DurSec: 1300, Head: h})
	}
	return eps
}

func TestIntros(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	eps := season(r, []float64{60, 75, 90, 62, -1}, 45)
	got := FindIntros(eps)
	want := map[int64]float64{100: 60, 101: 75, 102: 90, 103: 62}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for f, at := range want {
		s := got[f]
		if !near(s.Start, at, 0.3) || !near(s.Len(), 45, 0.5) {
			t.Errorf("file %d: %+v, want %.0f + 45 s", f, s, at)
		}
	}
}

func TestIntrosShortSharedStartIsNoIntro(t *testing.T) {
	// After Life S01: ~9 s shared at 0:00, no theme
	r := rand.New(rand.NewSource(3))
	if got := FindIntros(season(r, []float64{0, 0, 0, 0}, 9)); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestIntrosSilenceIsNoIntro(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	eps := season(r, []float64{-1, -1, -1}, 1)
	for i := range eps {
		for k := items(10); k < items(50); k++ {
			eps[i].Head[k] = 0x5555aaaa // a long hush: the same item over and over
		}
	}
	if got := FindIntros(eps); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func TestIntrosSameEpisodeFilesNotCompared(t *testing.T) {
	// two versions of one episode share everything: no intro from that
	r := rand.New(rand.NewSource(5))
	h := noise(r, 300)
	eps := []Episode{
		{File: 1, Item: 7, Number: 1, Head: h},
		{File: 2, Item: 7, Number: 1, Head: blur(r, h)},
	}
	if got := FindIntros(eps); len(got) != 0 {
		t.Fatalf("got %+v", got)
	}
}

func tailSeason(r *rand.Rand, dur float64, endAt []float64, endSec float64) []Episode {
	outro := noise(r, endSec)
	var eps []Episode
	at := dur - tailSec
	for i, e := range endAt {
		tl := noise(r, tailSec)
		if e >= 0 {
			put(r, tl, e-at, outro)
		}
		eps = append(eps, Episode{File: int64(200 + i), Item: int64(20 + i), Number: i + 1, DurSec: dur, Tail: tl, TailAt: at})
	}
	return eps
}

func TestCredits(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	dur := 1300.0
	eps := tailSeason(r, dur, []float64{1245, 1250, 1200, -1}, 45)
	eps[1].Black = []Span{{1243, 1245}} // a fade to black just before the music
	eps[3].Black = []Span{{1240, 1270}, {1271, 1299}}
	got := FindCredits(eps)
	if s := got[200]; !near(s.Start, 1245, 0.3) || s.End != dur {
		t.Errorf("ep1 %+v", s)
	}
	if s := got[201]; !near(s.Start, 1243, 0.01) || s.End != dur {
		t.Errorf("ep2 (black snap) %+v", s)
	}
	// a scene after the credits: their end stays
	if s := got[202]; !near(s.Start, 1200, 0.3) || !near(s.End, 1245, 0.5) {
		t.Errorf("ep3 (post-credits scene) %+v", s)
	}
	// no shared audio: black frames to the end
	if s := got[203]; s.Start != 1240 || s.End != dur {
		t.Errorf("ep4 (black) %+v", s)
	}
}

func TestBlackCredits(t *testing.T) {
	cases := []struct {
		black []Span
		ok    bool
		start float64
	}{
		{[]Span{{6000, 6105}}, true, 6000},
		{[]Span{{6000, 6050}, {6051.5, 6110}}, true, 6000}, // flicker joined
		{[]Span{{6000, 6020}}, false, 0},                   // too short
		{[]Span{{6000, 6070}}, false, 0},                   // ends 50 s early: a scene follows, ambiguous
		{nil, false, 0},
	}
	for i, c := range cases {
		s, ok := BlackCredits(c.black, 5220, 6120)
		if ok != c.ok || (ok && (s.Start != c.start || s.End != 6120)) {
			t.Errorf("%d: %+v %v", i, s, ok)
		}
	}
}

func TestStingsAndRecap(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	sting := noise(r, 3)
	eps := season(r, []float64{70, 80, 65}, 40)
	for i := range eps[:2] {
		put(r, eps[i].Head, 1, sting)
	}
	intros := FindIntros(eps)
	st := FindStings(eps, intros)
	if len(st) != 2 || !near(st[100], 4, 0.3) || !near(st[101], 4, 0.3) {
		t.Fatalf("stings %+v", st)
	}
	rc, ok := RecapAfter(st[100], intros[100], []Span{{10, 10.5}, {40, 42.5}, {55, 58}})
	if !ok || rc.Start != 0 || rc.End != 42.5 {
		t.Fatalf("recap %+v %v", rc, ok)
	}
	// the black run after the intro start: no recap
	if _, ok := RecapAfter(st[100], Span{30, 70}, []Span{{40, 42.5}}); ok {
		t.Fatal("recap past the intro")
	}
	// too short
	if _, ok := RecapAfter(4, Span{60, 100}, []Span{{8, 11}}); ok {
		t.Fatal("recap under 15 s")
	}
}

func BenchmarkSharedRuns10min(b *testing.B) {
	r := rand.New(rand.NewSource(8))
	x, y := noise(r, 600), noise(r, 600)
	for b.Loop() {
		sharedRuns(x, y, items(15), items(120))
	}
}

// 2 Broke Girls: an 11 s title card after a cold open is an intro when
// several episodes agree; the same 11 s shared by one pair alone is not.
func TestShortIntroAfterColdOpen(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	got := FindIntros(season(r, []float64{89.3, 112, 95, 101}, 11))
	if len(got) != 4 {
		t.Fatalf("got %+v", got)
	}
	for f, s := range got {
		if !near(s.Len(), 11, 0.5) {
			t.Errorf("file %d: %+v", f, s)
		}
	}
	r = rand.New(rand.NewSource(12))
	if got := FindIntros(season(r, []float64{89.3, 112, -1, -1}, 11)); len(got) != 0 {
		t.Fatalf("one pair: %+v", got)
	}
}

// A recurring 10 s cue 400 s before the end and the credits 60 s before it:
// the credits start at the credits, not at the cue.
func TestCreditsIgnoreEarlierSharedCue(t *testing.T) {
	r := rand.New(rand.NewSource(13))
	dur := 1300.0
	at := dur - tailSec
	cue, outro := noise(r, 10), noise(r, 60)
	var eps []Episode
	for i := 0; i < 4; i++ {
		tl := noise(r, tailSec)
		put(r, tl, dur-400-at, cue)
		put(r, tl, dur-60-at, outro)
		eps = append(eps, Episode{File: int64(300 + i), Item: int64(30 + i), Number: i + 1, DurSec: dur, Tail: tl, TailAt: at})
	}
	got := FindCredits(eps)
	for _, e := range eps {
		if s := got[e.File]; !near(s.Start, dur-60, 0.5) || s.End != dur {
			t.Errorf("file %d: %+v", e.File, s)
		}
	}
}

// With three or more episodes, credits need two neighbours agreeing: one
// pair sharing end audio is not enough.
func TestCreditsNeedTwoNeighbours(t *testing.T) {
	r := rand.New(rand.NewSource(14))
	eps := tailSeason(r, 1300, []float64{1250, 1250, -1, -1, -1}, 50)
	got := FindCredits(eps)
	if _, ok := got[200]; ok {
		t.Fatalf("one pair: %+v", got)
	}
	// three agree: each has two neighbours with it
	r = rand.New(rand.NewSource(15))
	eps = tailSeason(r, 1300, []float64{1250, 1245, 1240, -1}, 45)
	got = FindCredits(eps)
	if s := got[201]; !near(s.Start, 1245, 0.5) {
		t.Fatalf("three agree: %+v", got)
	}
}

// A recap is clips of earlier episodes: audio after the sting that an
// earlier episode has. A cold open after a shared logo is not one.
func TestRecapNeedsEarlierClips(t *testing.T) {
	r := rand.New(rand.NewSource(16))
	eps := season(r, []float64{70, 80, 65}, 40)
	// E3 opens with 20 s of E1's own scene (at 200 s in E1's head)
	clip := eps[0].Head[items(200):items(220)]
	put(r, eps[2].Head, 8, clip)
	if !EarlierClip(eps, eps[2].File, 4, 40) {
		t.Fatal("a clip of E1 in E3's recap not found")
	}
	if EarlierClip(eps, eps[1].File, 4, 40) {
		t.Fatal("E2's cold open taken for clips")
	}
	// a clip only a LATER episode has does not count
	if EarlierClip(eps, eps[0].File, 195, 225) {
		t.Fatal("a later episode's copy counted")
	}
}
