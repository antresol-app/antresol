package release

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// studio is a voice-over studio: its canonical name and the other ways
// release names spell it (case, ё/е and separators do not matter).
type studio struct {
	name    string
	aliases []string
}

// coreStudios: the studios every build knows, in suggestion order (the web
// settings copy this list).
var coreStudios = []studio{
	{"LostFilm", []string{"lost film", "лостфильм", "лост фильм"}},
	{"Кубик в Кубе", []string{"kubik v kube", "kubik3", "кубик³", "kubik³", "кубик3"}},
	{"NewStudio", []string{"new studio"}},
	{"AlexFilm", []string{"alex film"}},
	{"Jaskier", []string{"яськьер"}},
	{"TVShows", []string{"tv shows"}},
	{"Кураж-Бамбей", []string{"кураж бамбей", "kurazh-bambey", "kurazh bambey"}},
	{"Red Head Sound", []string{"rhs", "redheadsound"}},
	{"BaibaKo", []string{"байбако"}},
	{"Pifagor", []string{"пифагор"}},
	{"Novamedia", []string{"nova media"}},
	{"SDI Media", []string{"sdimedia"}},
	{"Amedia", nil},
	{"Coldfilm", []string{"cold film"}},
	{"IdeaFilm", []string{"idea film"}},
}

// studios in suggestion order (KnownStudios): coreStudios, plus what a
// build adds in an init (which then compiles studioRes again).
var studios = slices.Clone(coreStudios)

// studioRes: one word-bounded regexp per studio, all its spellings;
// studioKeys: the folded spelling -> canonical name.
var studioRes, studioKeys = compileStudios()

func compileStudios() ([]*regexp.Regexp, map[string]string) {
	res := make([]*regexp.Regexp, len(studios))
	keys := map[string]string{}
	for i, s := range studios {
		var alts []string
		for _, a := range append([]string{s.name}, s.aliases...) {
			keys[studioFold(a)] = s.name
			words := strings.FieldsFunc(foldYo(strings.ToLower(a)), func(r rune) bool {
				return r == ' ' || r == '-' || r == '.' || r == '_'
			})
			for j, w := range words {
				words[j] = regexp.QuoteMeta(w)
			}
			// "Lost Film", "LostFilm", "Lost.Film", "Кураж-Бамбей"
			alts = append(alts, strings.Join(words, `[\s._\-]*`))
		}
		res[i] = regexp.MustCompile(`(?i)` + lb + `(` + strings.Join(alts, "|") + `)` + rb)
	}
	return res, keys
}

// foldYo spells ё as е (same length in UTF-8, so offsets stay valid).
func foldYo(s string) string { return strings.NewReplacer("ё", "е", "Ё", "Е").Replace(s) }

// studioFold: lower case, ё = е, letters and digits only.
func studioFold(s string) string {
	var b strings.Builder
	for _, r := range foldYo(strings.ToLower(s)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Studios returns the canonical names of the voice-over studios named in
// s, in order of appearance, each once.
func Studios(s string) []string {
	s = foldYo(s)
	type hit struct {
		at   int
		name string
	}
	var hits []hit
	for i, re := range studioRes {
		if m := re.FindStringSubmatchIndex(s); m != nil {
			hits = append(hits, hit{m[2], studios[i].name})
		}
	}
	if len(hits) == 0 {
		return nil
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].at < hits[b].at })
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.name
	}
	return out
}

// CanonicalStudio returns the canonical name of a studio name or alias
// (case-insensitive, ё = е, separators ignored); false when unknown.
func CanonicalStudio(name string) (string, bool) {
	k := studioFold(name)
	if k == "" {
		return "", false
	}
	c, ok := studioKeys[k]
	return c, ok
}

// StudioKey identifies a studio name for comparing: the canonical name
// of a known studio, else the name folded (lower case, ё = е, letters and
// digits only).
func StudioKey(name string) string {
	if c, ok := CanonicalStudio(name); ok {
		return c
	}
	return studioFold(name)
}

// NamesStudio reports whether s names the studio name as whole words
// (case-insensitive, ё = е; separators between its words may vary). For
// studios outside the table, typed by the household.
func NamesStudio(s, name string) bool {
	words := strings.FieldsFunc(foldYo(strings.ToLower(name)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	if len(words) == 0 {
		return false
	}
	for i, w := range words {
		words[i] = regexp.QuoteMeta(w)
	}
	re, err := regexp.Compile(`(?i)` + lb + strings.Join(words, `[^\p{L}\p{N}]*`) + rb)
	return err == nil && re.MatchString(foldYo(s))
}

// KnownStudios returns the canonical names of the known studios in
// suggestion order.
func KnownStudios() []string {
	out := make([]string, len(studios))
	for i, s := range studios {
		out[i] = s.name
	}
	return out
}

// MergeStudios merges studio lists in order (the series' own first), each
// studio once (by StudioKey), under its canonical name.
func MergeStudios(lists ...[]string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lists {
		for _, s := range l {
			s = strings.TrimSpace(s)
			k := StudioKey(s)
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			if c, ok := CanonicalStudio(s); ok {
				s = c
			}
			out = append(out, s)
		}
	}
	return out
}
