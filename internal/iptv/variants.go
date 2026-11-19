package iptv

import (
	"strings"
	"unicode"
)

// Variants of one channel: a big playlist carries the same channel in
// several qualities and regional feeds («Пример HD», «Пример FHD»,
// «Пример UHD», «Пример HD 50», «Пример orig», «Пример +0 (Регион)»). They
// are one channel to a viewer: a source's variants share one logical row,
// the best one is its primary stream and the others its fallbacks (the
// players go down the list when a stream fails), and the editor shows them
// hidden with WhyVariant. Timeshift copies are not variants (another
// time, not another picture), nor are adult channels.

// WhyVariant: shown as another stream of the same channel, not as a
// channel of its own (channels.why; a catalog key).
const WhyVariant = "пресет: вариант канала"

// Quality of a variant, best first for the default pick. Full HD and HD
// lead: a UHD feed is usually HEVC at a bit rate a TV on Wi-Fi or a
// browser cannot keep up with, so it comes after them (still before a
// plain or SD feed) and stays reachable as a fallback.
const (
	qualSD    = 10
	qualPlain = 20
	qualUHD   = 30
	qualHD    = 40
	qualFHD   = 50
)

var variantQuality = map[string]int{
	"sd": qualSD, "lq": qualSD, "480p": qualSD, "576p": qualSD,
	"hd": qualHD, "hq": qualHD, "720p": qualHD, "hdr": qualHD,
	"fhd": qualFHD, "1080": qualFHD, "1080p": qualFHD, "1080i": qualFHD,
	"uhd": qualUHD, "4k": qualUHD, "8k": qualUHD, "2160p": qualUHD,
}

// Marks that carry no quality but still say «the same channel».
var variantPlain = map[string]int{
	"orig": 2, "original": 2, "оригинал": 2, // the provider's original feed
	"+0":   -5,                                  // a regional feed shifted by nothing
	"hevc": -4, "h265": -4, "h264": 0, "avc": 0, // HEVC: not every browser plays it
	"50fps": 3, "60fps": 3, "fps": 0,
}

// variantWords splits a name the way NormName does (lower case, ё→е,
// punctuation folded, '+' kept), keeping parentheses as their own words so
// a trailing «(Регион)» can be told apart.
func variantWords(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case r == 'ё':
			b.WriteRune('е')
		case r == '+' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '(' || r == ')' || r == '[' || r == ']':
			flush()
			out = append(out, string(r))
		default:
			flush()
		}
	}
	flush()
	return out
}

// VariantName is a name's base channel and how good this variant of it
// is: «Пример HD 50» → («пример», HD + 50 fps), «Пример +0 (Регион)» →
// («пример», a regional feed). The base keeps at least one word, so a name
// that is only a mark («HD») is its own base.
func VariantName(name string) (base string, rank int) {
	base, rank, _ = variantParse(name)
	return base, rank
}

// variantParse is VariantName and whether a trailing (…) read as a place
// (a regional feed — or a timeshift or another channel the rules cannot
// tell from one: the AI helper is asked, variants_ai.go).
func variantParse(name string) (base string, rank int, regional bool) {
	w := variantWords(name)
	quality, bonus := qualPlain, 0
	for {
		n := len(w)
		switch {
		case n >= 2 && (w[n-1] == ")" || w[n-1] == "]"):
			// a trailing (…): a region or a quality note
			open := "("
			if w[n-1] == "]" {
				open = "["
			}
			i := n - 2
			for i >= 0 && w[i] != open {
				i--
			}
			if i <= 0 { // unbalanced, or the whole name in brackets
				goto done
			}
			inner := w[i+1 : n-1]
			if q, ok := marksOnly(inner); ok {
				quality, bonus = max(quality, q.quality), bonus+q.bonus
			} else if regionWords(inner) {
				bonus -= 5 // a regional feed: the national one leads
				regional = true
			} else {
				goto done
			}
			w = w[:i]
		case n >= 2:
			last := w[n-1]
			if q, ok := variantQuality[last]; ok {
				quality = max(quality, q)
			} else if b, ok := variantPlain[last]; ok {
				bonus += b
			} else if (last == "50" || last == "60") && n >= 3 && variantQuality[w[n-2]] != 0 {
				bonus += 3 // «HD 50»: 50 frames a second
			} else {
				goto done
			}
			w = w[:n-1]
		default:
			goto done
		}
	}
done:
	clean := w[:0:0]
	for _, x := range w {
		if x != "(" && x != ")" && x != "[" && x != "]" {
			clean = append(clean, x)
		}
	}
	return strings.Join(clean, " "), quality + bonus, regional
}

type markSum struct{ quality, bonus int }

// marksOnly: every word in brackets is a variant mark («(HD)», «[FHD 50]»).
func marksOnly(ws []string) (markSum, bool) {
	s := markSum{}
	if len(ws) == 0 {
		return s, false
	}
	for _, x := range ws {
		if q, ok := variantQuality[x]; ok {
			s.quality = max(s.quality, q)
		} else if b, ok := variantPlain[x]; ok {
			s.bonus += b
		} else if x != "50" && x != "60" {
			return s, false
		}
	}
	return s, true
}

// regionWords: words in brackets that read as a place («(Регион)», «(Санкт
// Петербург)»): one to three words of letters. Digits («(2)») are not.
func regionWords(ws []string) bool {
	if len(ws) == 0 || len(ws) > 3 {
		return false
	}
	for _, x := range ws {
		for _, r := range x {
			if !unicode.IsLetter(r) {
				return false
			}
		}
	}
	return true
}

// variantCandidate: an entry that can be a variant of another (not a
// timeshift copy, not an adult channel).
func variantCandidate(m Member) bool {
	return !IsTimeshift(m.Name) && !IsAdult(m.Name, m.Group)
}

// maxIDShare: a tvg-id or guide id shared by more than this many
// different base names is a playlist's placeholder, not one channel.
const maxIDShare = 3

// VariantGroups groups a source's present entries into variants of one
// channel by the rules alone: the same base name (VariantName), or the same
// tvg-id or guide channel. Each group of two or more is returned best
// first (rank, then playlist position); entries of no group are left out.
// The AI helper's answers and the admin's word come on top (planVariants).
func VariantGroups(ms []Member) [][]Member {
	return planVariants(ms, nil).groups
}
