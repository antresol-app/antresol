// Package segments finds the parts of an episode or a film the player
// offers to skip: the recap («Ранее в сериале»), the intro (opening titles)
// and the credits. They come, in order of priority, from the file's
// chapters, the online databases (TheIntroDB, IntroDB) and our own analysis
// of the audio (chromaprint) and picture (blackdetect) of a season's
// episodes. Results are kept per file and source (file_segments); the play
// response reads what is stored and never waits for a lookup.
package segments

import (
	"sort"
)

// Kinds of segment.
const (
	Recap   = "recap"
	Intro   = "intro"
	Credits = "credits"
)

// Sources, best first.
const (
	SourceChapters   = "chapters"
	SourceTheIntroDB = "theintrodb"
	SourceIntroDB    = "introdb"
	SourceAnalysis   = "analysis"
)

// sourceRank orders the sources: lower wins.
var sourceRank = map[string]int{SourceChapters: 0, SourceTheIntroDB: 1, SourceIntroDB: 2, SourceAnalysis: 3}

// Segment is one stored segment of a file, in ms on the file timeline.
type Segment struct {
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
}

// Play is a segment as POST /api/play returns it: seconds on the file
// timeline (the clock of the subtitles).
type Play struct {
	Kind     string  `json:"kind"`
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
}

// MinPlayMs: shorter segments are not offered (a stray chapter mark).
const MinPlayMs = 3000

// Pick chooses what the player gets from the stored segments of a file:
// per kind the best source, nothing shorter than MinPlayMs, a film's
// credits only, clipped to the file (durationMs 0: unknown, not clipped),
// in time order. Never nil.
func Pick(stored []Segment, durationMs int64, movie bool) []Play {
	best := map[string]Segment{}
	for _, s := range stored {
		if movie && s.Kind != Credits {
			continue
		}
		if durationMs > 0 && s.EndMs > durationMs {
			s.EndMs = durationMs
		}
		if s.StartMs < 0 {
			s.StartMs = 0
		}
		if s.EndMs-s.StartMs < MinPlayMs {
			continue
		}
		if b, ok := best[s.Kind]; !ok || sourceRank[s.Source] < sourceRank[b.Source] {
			best[s.Kind] = s
		}
	}
	out := make([]Play, 0, len(best))
	for _, s := range best {
		out = append(out, Play{Kind: s.Kind, StartSec: float64(s.StartMs) / 1000, EndSec: float64(s.EndMs) / 1000})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartSec < out[j].StartSec })
	return out
}

// covered reports whether segs (of sources better than the analysis) already
// give everything the databases or the analysis could add: recap, intro and
// credits for an episode (most have no recap, so an episode is rarely
// covered: a chapter-marked intro still leaves the recap to look for),
// credits for a film.
func covered(segs []Segment, movie bool) bool {
	has := map[string]bool{}
	for _, s := range segs {
		if s.Source != SourceAnalysis {
			has[s.Kind] = true
		}
	}
	if movie {
		return has[Credits]
	}
	return has[Recap] && has[Intro] && has[Credits]
}

// basicCovered: segs (of sources better than the analysis) give an
// episode's intro and credits; only a recap may still be missing.
func basicCovered(segs []Segment) bool {
	has := map[string]bool{}
	for _, s := range segs {
		if s.Source != SourceAnalysis {
			has[s.Kind] = true
		}
	}
	return has[Intro] && has[Credits]
}
