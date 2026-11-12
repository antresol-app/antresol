package iptv

import (
	"strings"
	"unicode"
)

// qualityMarks are dropped from the end of a normalised name.
var qualityMarks = map[string]bool{"hd": true, "fhd": true, "uhd": true, "4k": true, "sd": true, "hdr": true, "orig": true, "50fps": true}

func yoToYe(r rune) rune {
	if r == 'ё' {
		return 'е'
	}
	return r
}

// fold is a name compared "exactly": lower case, spaces collapsed.
func fold(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// NormName is a channel name for loose comparison: lower case, ё→е,
// punctuation and spaces folded to single spaces ('+' kept, so «+2»
// copies stay apart), and the quality marks hd fhd uhd 4k sd hdr orig
// 50fps dropped while they are whole words at the end (never the whole
// name).
func NormName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r == 'ё':
			r = 'е'
		case r == '+' || unicode.IsLetter(r) || unicode.IsDigit(r):
		default:
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	words := strings.Split(b.String(), " ")
	for len(words) > 1 && qualityMarks[words[len(words)-1]] {
		words = words[:len(words)-1]
	}
	return strings.Join(words, " ")
}

// Matcher finds a playlist entry's channel in a guide (spec goal 3):
// tvg-id, then an exact display-name, then a normalised one when the
// playlist has no other entry with the same normalised name. A manual
// binding is the caller's and never goes through it.
type Matcher struct {
	ids     map[string]string // id → id
	idsFold map[string]string // lower id → first id
	exact   map[string]string // fold(display-name) → first id
	norm    map[string]string // NormName(display-name) → id
}

// NewMatcher indexes a guide's channels (first in guide order wins).
func NewMatcher(chans []XMLChannel) *Matcher {
	m := &Matcher{
		ids:     make(map[string]string, len(chans)),
		idsFold: make(map[string]string, len(chans)),
		exact:   make(map[string]string, 2*len(chans)),
		norm:    make(map[string]string, 2*len(chans)),
	}
	normExact := map[string]bool{} // a norm entry whose name was already normal
	for _, c := range chans {
		if c.ID == "" {
			continue
		}
		if _, ok := m.ids[c.ID]; !ok {
			m.ids[c.ID] = c.ID
		}
		if l := strings.ToLower(c.ID); m.idsFold[l] == "" {
			m.idsFold[l] = c.ID
		}
		for _, n := range c.Names {
			f := fold(n)
			if _, ok := m.exact[f]; !ok && f != "" {
				m.exact[f] = c.ID
			}
			nn := NormName(n)
			if nn == "" {
				continue
			}
			// «Земля» wins «Земля 4K» for the norm «земля».
			plain := fold(strings.Map(yoToYe, n)) == nn
			if _, ok := m.norm[nn]; !ok || (plain && !normExact[nn]) {
				m.norm[nn] = c.ID
				normExact[nn] = plain
			}
		}
	}
	return m
}

// MatchResult is a guide channel id and how it was found (tvg-id, name,
// norm-name; "" with no match).
type MatchResult struct {
	XMLID string
	How   string
}

func (m *Matcher) direct(mb Member) (MatchResult, bool) {
	if mb.TvgID != "" {
		if id, ok := m.ids[mb.TvgID]; ok {
			return MatchResult{id, "tvg-id"}, true
		}
		if id := m.idsFold[strings.ToLower(mb.TvgID)]; id != "" {
			return MatchResult{id, "tvg-id"}, true
		}
	}
	for _, n := range []string{mb.TvgName, mb.Name} {
		if f := fold(n); f != "" {
			if id, ok := m.exact[f]; ok {
				return MatchResult{id, "name"}, true
			}
		}
	}
	return MatchResult{}, false
}

// Match finds a member's guide channel. siblings: the names of the other
// entries of the playlist (an entry whose name normalises to the member's
// but differs from it blocks the normalised match).
func (m *Matcher) Match(mb Member, siblings []string) (xmlID string, how string) {
	if r, ok := m.direct(mb); ok {
		return r.XMLID, r.How
	}
	for _, n := range []string{mb.TvgName, mb.Name} {
		nn := NormName(n)
		if nn == "" {
			continue
		}
		id, ok := m.norm[nn]
		if !ok {
			continue
		}
		f, collides := fold(n), false
		for _, s := range siblings {
			if fold(s) != f && NormName(s) == nn {
				collides = true
				break
			}
		}
		if !collides {
			return id, "norm-name"
		}
	}
	return "", ""
}

// MatchAll matches a whole playlist (each member's siblings are the
// others), in one pass over the names.
func (m *Matcher) MatchAll(ms []Member) []MatchResult {
	// Distinct folded names per normalised name.
	variants := map[string]map[string]bool{}
	add := func(n string) {
		nn := NormName(n)
		if nn == "" {
			return
		}
		v := variants[nn]
		if v == nil {
			v = map[string]bool{}
			variants[nn] = v
		}
		v[fold(n)] = true
	}
	for _, mb := range ms {
		add(mb.Name)
		if mb.TvgName != "" && fold(mb.TvgName) != fold(mb.Name) {
			add(mb.TvgName)
		}
	}
	out := make([]MatchResult, len(ms))
	for i, mb := range ms {
		if r, ok := m.direct(mb); ok {
			out[i] = r
			continue
		}
		for _, n := range []string{mb.TvgName, mb.Name} {
			nn := NormName(n)
			id, ok := m.norm[nn]
			if nn == "" || !ok {
				continue
			}
			// The member's own names are its variants too; any other blocks.
			own := map[string]bool{fold(mb.Name): true, fold(mb.TvgName): true}
			collides := false
			for v := range variants[nn] {
				if !own[v] {
					collides = true
					break
				}
			}
			if !collides {
				out[i] = MatchResult{id, "norm-name"}
				break
			}
		}
	}
	return out
}
