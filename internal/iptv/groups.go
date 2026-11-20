package iptv

import (
	"strings"
	"unicode"
)

// Group-titles as tags: a playlist spells one topic many ways («RU | Кино
// HD», «Фильмы», «Movies», «КИНОХИТ»), and the AI helper's categories
// (categories.go) name the same topics again. GroupCodes reads a group-title
// as the codes of CategoryList it means, so «Кино» of a group and «Кино» of
// the AI are one tag; a group of none of them stays a tag of its own,
// cleaned (CleanGroup) so its spellings meet too.

// AdultCode is the code of the adult category: exclusive (its channels are
// in no other category) and always last.
const AdultCode = "adult"

// groupStems: the stems of each code (lower case, matched at a word's
// start), in CategoryList's order.
var groupStems = []struct {
	code  string
	stems []string
}{
	{"news", []string{"новост", "новостн", "news", "инфо", "info"}},
	{"sport", []string{"спорт", "sport", "футбол", "football", "soccer", "хоккей", "hockey", "бокс", "fight"}},
	{"movies", []string{"кино", "фильм", "movie", "film", "cinema", "кинохит", "vod"}},
	{"series", []string{"сериал", "series", "tv show", "tvshow"}},
	{"kids", []string{"детск", "дети", "детям", "мульт", "kids", "children", "child", "cartoon", "family kids"}},
	{"music", []string{"музык", "music", "клип", "radio", "радио"}},
	{"education", []string{"познават", "образоват", "научн", "наука", "science", "education", "educational", "knowledge"}},
	{"documentary", []string{"документ", "docu"}},
	{"entertainment", []string{"развлека", "entertainment", "шоу", "show"}},
	{"humor", []string{"юмор", "humor", "humour", "comedy", "комеди"}},
	{"lifestyle", []string{"стиль жизни", "lifestyle", "life style", "кулинар", "cooking", "food", "хобби", "hobby"}},
	{"travel", []string{"путешеств", "travel", "туризм", "природ", "nature"}},
	{"business", []string{"бизнес", "business", "эконом", "финанс", "finance"}},
	{"religion", []string{"религ", "religion", "religious", "правосл", "faith"}},
	{"regional", []string{"регион", "regional", "local", "местн", "городск"}},
	{"general", []string{"общие", "общероссийск", "эфирн", "федеральн", "general", "основн", "main"}},
	{"shopping", []string{"магазин", "shop", "телемагазин"}},
}

// noise: words of a group-title that say nothing of its topic (quality,
// country and language marks).
var noise = map[string]bool{
	"hd": true, "fhd": true, "uhd": true, "sd": true, "4k": true, "8k": true, "hq": true, "hevc": true, "h265": true,
	"ru": true, "rus": true, "russia": true, "россия": true, "ua": true, "by": true, "kz": true, "en": true, "eng": true, "us": true, "uk": true,
	"tv": true, "тв": true, "каналы": true, "channels": true, "канал": true, "channel": true,
}

// words: s in lower case cut at anything not a letter, a digit or «+».
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '+'
	})
}

// meaningful: the words of g without noise.
func meaningful(g string) []string {
	var out []string
	for _, w := range words(g) {
		if !noise[w] {
			out = append(out, w)
		}
	}
	return out
}

// GroupCodes: the CategoryList codes group-title g means (none: a topic of
// its own), the adult code for an adult group (alone: it is exclusive).
func GroupCodes(g string) []string {
	if IsAdultGroup(g) {
		return []string{AdultCode}
	}
	ws := meaningful(g)
	if len(ws) == 0 {
		return nil
	}
	text := " " + strings.Join(ws, " ")
	var out []string
	for _, c := range groupStems {
		for _, s := range c.stems {
			if strings.Contains(text, " "+s) {
				out = append(out, c.code)
				break
			}
		}
	}
	return out
}

// CleanGroup: g without its noise words and odd punctuation, as one
// spelling («RU | НОВОСТНЫЕ HD» → «Новостные»): "" when nothing is left.
func CleanGroup(g string) string {
	var kept []string
	for _, f := range strings.FieldsFunc(g, func(r rune) bool {
		return unicode.IsSpace(r) || r == '|' || r == '/' || r == '\\' || r == ':' || r == '[' || r == ']' || r == '(' || r == ')' || r == '•' || r == '·'
	}) {
		f = strings.TrimFunc(f, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '+' })
		if f == "" || noise[strings.ToLower(f)] {
			continue
		}
		kept = append(kept, f)
	}
	s := strings.Join(kept, " ")
	if s == "" {
		return ""
	}
	if s == strings.ToUpper(s) && len([]rune(s)) > 4 { // «НОВОСТНЫЕ»: as a sentence
		s = strings.ToLower(s)
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}
