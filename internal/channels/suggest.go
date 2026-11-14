package channels

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Suggestion is a channel made from a sentence («создай канал с советскими
// фильмами»): its name and its rules, for the editor to show as tags before
// anything is saved.
type Suggestion struct {
	Name  string `json:"name"`
	Rules Rules  `json:"rules"`
	// Via: "words" (the words alone) or "ai" (the model helped).
	Via string `json:"via"`
}

// Vocabulary is what the library has: a suggestion keeps only these (a
// genre, a country or a theme nothing has would make an empty channel).
type Vocabulary struct {
	Genres    map[string]bool // GenreKey
	Countries map[string]bool // ISO 3166-1, upper case
	Languages map[string]bool // ISO 639-1
	Keywords  map[string]bool // lower case
}

// VocabularyOf is the vocabulary of the units.
func VocabularyOf(units []Unit) Vocabulary {
	v := Vocabulary{Genres: map[string]bool{}, Countries: map[string]bool{}, Languages: map[string]bool{}, Keywords: map[string]bool{}}
	for _, u := range units {
		for _, g := range u.Genres {
			v.Genres[g] = true
		}
		for _, c := range u.Countries {
			v.Countries[strings.ToUpper(c)] = true
		}
		if u.OrigLang != "" {
			v.Languages[u.OrigLang] = true
		}
		for _, k := range u.Keywords {
			v.Keywords[strings.ToLower(k)] = true
		}
	}
	return v
}

// Suggest reads a sentence into a channel. The words it knows (countries,
// genres, years, «фильмы» / «сериалы», a few themes) always count; the
// model, when there is one, adds themes and a name and fills what the words
// left open. A model that fails or is slow leaves the words' answer.
func (s *Service) Suggest(ctx context.Context, text string, vocab Vocabulary) Suggestion {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) > 300 {
		text = string([]rune(text)[:300])
	}
	out := suggestWords(text)
	out.Via = "words"
	if s.LLM != nil && text != "" {
		ctx, cancel := context.WithTimeout(ctx, s.LLM.timeout())
		defer cancel()
		if ai, err := s.LLM.suggest(ctx, text, vocab); err == nil {
			out = mergeSuggestion(out, ai)
			out.Via = "ai"
		}
	}
	out.Rules = keepKnown(out.Rules, vocab)
	if out.Name == "" {
		out.Name = nameOf(text)
	}
	return out
}

// ---- the words ----

// countryWords: the stems that name a country (adjectives and names, Russian and English).
var countryWords = []struct {
	code  string
	stems []string
}{
	{"SU", []string{"советск", "ссср", "soviet", "ussr"}},
	{"RU", []string{"российск", "русск", "россии", "россия", "russia"}},
	{"US", []string{"американск", "сша", "голливуд", "america", "usa", "hollywood"}},
	{"GB", []string{"британск", "английск", "англии", "великобритан", "british", "england", "english film", "uk "}},
	{"FR", []string{"французск", "франци", "french", "france"}},
	{"IT", []string{"итальянск", "итали", "italian", "italy"}},
	{"DE", []string{"немецк", "германи", "german"}},
	{"ES", []string{"испанск", "испани", "spanish", "spain"}},
	{"JP", []string{"японск", "япони", "japanese", "japan"}},
	{"KR", []string{"корейск", "южной коре", "korean", "korea"}},
	{"CN", []string{"китайск", "китай", "chinese", "china"}},
	{"HK", []string{"гонконг", "hong kong"}},
	{"IN", []string{"индийск", "болливуд", "индии", "indian", "bollywood", "india"}},
	{"SE", []string{"шведск", "швеци", "swedish", "sweden"}},
	{"DK", []string{"датск", "danish", "denmark"}},
	{"NO", []string{"норвежск", "норвеги", "norwegian", "norway"}},
	{"FI", []string{"финск", "финлянд", "finnish", "finland"}},
	{"PL", []string{"польск", "польш", "polish", "poland"}},
	{"CZ", []string{"чешск", "чехи", "czech"}},
	{"XC", []string{"чехословацк", "czechoslovak"}},
	{"YU", []string{"югославск", "yugoslav"}},
	{"DD", []string{"гдр", "east german"}},
	{"UA", []string{"украинск", "украин", "ukrainian", "ukraine"}},
	{"GE", []string{"грузинск", "грузии", "georgian"}},
	{"KZ", []string{"казахск", "казахстан", "kazakh"}},
	{"CA", []string{"канадск", "канад", "canadian", "canada"}},
	{"AU", []string{"австралийск", "австрали", "australian", "australia"}},
	{"NZ", []string{"новозеландск", "новой зеланд", "new zealand"}},
	{"IE", []string{"ирландск", "ирланди", "irish", "ireland"}},
	{"BE", []string{"бельгийск", "бельги", "belgian", "belgium"}},
	{"NL", []string{"голландск", "нидерланд", "dutch", "netherlands"}},
	{"MX", []string{"мексиканск", "мексик", "mexican", "mexico"}},
	{"BR", []string{"бразильск", "бразили", "brazilian", "brazil"}},
	{"AR", []string{"аргентинск", "аргентин", "argentin"}},
	{"TR", []string{"турецк", "турци", "turkish", "turkey"}},
	{"IR", []string{"иранск", "iranian"}},
	{"GR", []string{"греческ", "греци", "greek", "greece"}},
	{"HU", []string{"венгерск", "венгри", "hungarian", "hungary"}},
	{"RO", []string{"румынск", "румыни", "romanian", "romania"}},
	{"AT", []string{"австрийск", "австри", "austrian", "austria"}},
	{"CH", []string{"швейцарск", "швейцари", "swiss", "switzerland"}},
	{"TH", []string{"тайск", "таиланд", "thai"}},
}

// genreWords: stems of a genre's name and the genre keys it means (a show's
// genres are other ones: «фантастика» is «НФ и Фэнтези» there). Longer
// stems first: «мелодрам» is not «драм».
var genreWords = []struct {
	stems []string
	keys  []string
}{
	{[]string{"мелодрам", "романтическ", "про любовь", "romance", "romantic"}, []string{"мелодрама"}},
	{[]string{"мульт", "анимац", "анимэ", "аниме", "animation", "animated", "cartoon", "anime"}, []string{"мультфильм"}},
	{[]string{"комеди", "смешн", "comed", "funny"}, []string{"комедия"}},
	{[]string{"боевик", "экшн", "action"}, []string{"боевик", "боевик и приключения"}},
	{[]string{"приключен", "adventure"}, []string{"приключения", "боевик и приключения"}},
	{[]string{"криминал", "гангстер", "crime", "gangster"}, []string{"криминал"}},
	{[]string{"документальн", "documentar"}, []string{"документальный"}},
	{[]string{"драм", "drama"}, []string{"драма"}},
	{[]string{"семейн", "для всей семьи", "family"}, []string{"семейный"}},
	{[]string{"детск", "для детей", "kids", "children"}, []string{"семейный", "детский", "мультфильм"}},
	{[]string{"фэнтези", "fantasy"}, []string{"фэнтези", "нф и фэнтези"}},
	{[]string{"историческ", "history", "historical"}, []string{"история"}},
	{[]string{"ужас", "хоррор", "страшн", "horror", "scary"}, []string{"ужасы"}},
	{[]string{"мюзикл", "музыкальн", "musical"}, []string{"музыка"}},
	{[]string{"детектив", "мистик", "mystery", "detective"}, []string{"детектив"}},
	{[]string{"фантастик", "sci-fi", "science fiction", "научно-фантаст"}, []string{"фантастика", "нф и фэнтези"}},
	{[]string{"триллер", "thriller"}, []string{"триллер"}},
	{[]string{"военн", "о войне", "про войну", "war film", "war movie"}, []string{"военный", "война и политика"}},
	{[]string{"вестерн", "western"}, []string{"вестерн"}},
}

// themeWords: themes the words know, as TMDB keywords.
var themeWords = []struct {
	stems []string
	key   string
}{
	{[]string{"космос", "космическ", "в космосе", "space"}, "space"},
	{[]string{"новогодн", "новый год", "рождеств", "christmas", "new year"}, "christmas"},
	{[]string{"зомби", "zombie"}, "zombie"},
	{[]string{"вампир", "vampire"}, "vampire"},
	{[]string{"супергеро", "superhero"}, "superhero"},
	{[]string{"путешестви во времени", "путешествия во времени", "time travel"}, "time travel"},
	{[]string{"динозавр", "dinosaur"}, "dinosaur"},
	{[]string{"пират", "pirate"}, "pirate"},
	{[]string{"робот", "robot"}, "robot"},
	{[]string{"акул", "shark"}, "shark"},
	{[]string{"ограблен", "heist"}, "heist"},
	{[]string{"шпион", "spy"}, "spy"},
	{[]string{"спорт", "sport"}, "sport"},
	{[]string{"пришельц", "инопланет", "alien"}, "alien"},
}

var (
	reRange   = regexp.MustCompile(`(1[89]\d\d|20\d\d)\s*(?:[-–—]|до|to)\s*(1[89]\d\d|20\d\d)`)
	reDecade4 = regexp.MustCompile(`(1[89]\d0|20\d0)\s*-?\s*(?:х|x|s|'s)\b`)
	reDecade2 = regexp.MustCompile(`(?:^|[^\d])([2-9]0|00|10|20)\s*-?\s*(?:х|x|s|'s)(?:\s|$|[^\p{L}])`)
	reFrom    = regexp.MustCompile(`(?:с|после|since|after|from)\s+(1[89]\d\d|20\d\d)`)
	reTo      = regexp.MustCompile(`(?:до|по|before|until)\s+(1[89]\d\d|20\d\d)`)
	reYear    = regexp.MustCompile(`(?:^|[^\d])(1[89]\d\d|20\d\d)(?:[^\d]|$)`)
	reVerb    = regexp.MustCompile(`^(?:пожалуйста,?\s+)?(?:создай|сделай|собери|запусти|хочу|нужен|make|create|build)(?:\s+(?:мне|me))?(?:\s+(?:новый|a|a new))?\s+`)
	reShows   = regexp.MustCompile(`^(?:тв-)?(?:канал|channel),?\s+(?:(?:который|которые|that|which)\s+)?(?:(?:будет|будут|will)\s+)?(?:транслировать|показывать|крутить|shows?|plays?|streams?)\s+`)
)

// suggestWords reads what the words of a sentence say.
func suggestWords(text string) Suggestion {
	t := " " + strings.ToLower(strings.ReplaceAll(text, "ё", "е")) + " "
	var r Rules
	for _, c := range countryWords {
		for _, st := range c.stems {
			if strings.Contains(t, st) {
				r.Countries = append(r.Countries, c.code)
				break
			}
		}
	}
	// «чехословацкие» is not also «чешские»
	if contains(r.Countries, "XC") {
		r.Countries = without(r.Countries, "CZ")
	}
	used := t
	for _, g := range genreWords {
		for _, st := range g.stems {
			if strings.Contains(used, st) {
				for _, k := range g.keys {
					if !contains(r.Genres, k) {
						r.Genres = append(r.Genres, k)
					}
				}
				used = strings.ReplaceAll(used, st, " ")
				break
			}
		}
	}
	for _, th := range themeWords {
		for _, st := range th.stems {
			if strings.Contains(t, st) {
				r.Keywords = append(r.Keywords, th.key)
				break
			}
		}
	}
	movies := hasAny(t, "фильм", "кино", "movie", "film")
	series := hasAny(t, "сериал", "series", "shows", " show", "tv show")
	switch {
	case movies && !series:
		r.Kinds = []string{"movie"}
	case series && !movies:
		r.Kinds = []string{"series"}
	}
	r.YearFrom, r.YearTo = yearsOf(t)
	return Suggestion{Rules: r}
}

// yearsOf: a range («1950–1991»), a decade («90-х», «1980s»), «с 2000» /
// «до 1960», or one year.
func yearsOf(t string) (from, to int) {
	if m := reRange.FindStringSubmatch(t); m != nil {
		return atoi(m[1]), atoi(m[2])
	}
	if m := reDecade4.FindStringSubmatch(t); m != nil {
		d := atoi(m[1])
		return d, d + 9
	}
	if m := reDecade2.FindStringSubmatch(t); m != nil {
		d := atoi(m[1])
		if d <= 20 {
			d += 2000
		} else {
			d += 1900
		}
		return d, d + 9
	}
	if m := reFrom.FindStringSubmatch(t); m != nil {
		from = atoi(m[1])
	}
	if m := reTo.FindStringSubmatch(t); m != nil {
		to = atoi(m[1])
	}
	if from != 0 || to != 0 {
		return from, to
	}
	if m := reYear.FindStringSubmatch(t); m != nil {
		y := atoi(m[1])
		return y, y
	}
	return 0, 0
}

// nameOf: the sentence without «создай канал с…», capitalised, short.
func nameOf(text string) string {
	n := strings.TrimSpace(text)
	// «создай канал, который будет показывать советские фильмы» → «советские фильмы»;
	// «создай канал с комедиями» → «канал с комедиями» (the case stays right)
	for _, re := range []*regexp.Regexp{reVerb, reShows} {
		if loc := re.FindStringIndex(strings.ToLower(n)); loc != nil && loc[1] < len(n) {
			n = strings.TrimSpace(n[loc[1]:])
		}
	}
	n = strings.TrimRight(n, ".!?, ")
	if r := []rune(n); len(r) > 40 {
		n = strings.TrimSpace(string(r[:40])) + "…"
	}
	if n == "" {
		return ""
	}
	r, size := utf8.DecodeRuneInString(n)
	return string(unicode.ToUpper(r)) + n[size:]
}

// mergeSuggestion: the words win where they found something; the model
// fills the rest and adds its themes.
func mergeSuggestion(words, ai Suggestion) Suggestion {
	out := words
	w, a := words.Rules, ai.Rules
	if len(w.Kinds) == 0 {
		out.Rules.Kinds = a.Kinds
	}
	if len(w.Genres) == 0 {
		out.Rules.Genres = a.Genres
	}
	if len(w.Countries) == 0 {
		out.Rules.Countries = a.Countries
	}
	if w.YearFrom == 0 && w.YearTo == 0 {
		out.Rules.YearFrom, out.Rules.YearTo = a.YearFrom, a.YearTo
	}
	if len(w.Languages) == 0 {
		out.Rules.Languages = a.Languages
	}
	for _, k := range a.Keywords {
		if !contains(out.Rules.Keywords, k) {
			out.Rules.Keywords = append(out.Rules.Keywords, k)
		}
	}
	if n := strings.TrimSpace(ai.Name); n != "" && utf8.RuneCountInString(n) <= 40 {
		out.Name = n
	}
	return out
}

// keepKnown drops what the library does not have (an empty vocabulary — a
// library the enricher has not reached yet — keeps everything).
func keepKnown(r Rules, v Vocabulary) Rules {
	keep := func(list []string, set map[string]bool) []string {
		if len(set) == 0 {
			return list
		}
		var out []string
		for _, x := range list {
			if set[x] {
				out = append(out, x)
			}
		}
		return out
	}
	r.Genres = keep(r.Genres, v.Genres)
	r.Countries = keep(cleanList(r.Countries, strings.ToUpper, 40), v.Countries)
	r.Languages = keep(cleanList(r.Languages, strings.ToLower, 40), v.Languages)
	r.Keywords = keep(cleanList(r.Keywords, strings.ToLower, 40), v.Keywords)
	if r.YearFrom != 0 && r.YearTo != 0 && r.YearFrom > r.YearTo {
		r.YearFrom, r.YearTo = r.YearTo, r.YearFrom
	}
	for _, y := range []*int{&r.YearFrom, &r.YearTo} {
		if *y != 0 && (*y < 1880 || *y > time.Now().Year()+1) {
			*y = 0
		}
	}
	var kinds []string
	for _, k := range r.Kinds {
		if (k == "movie" || k == "series") && !contains(kinds, k) {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 2 {
		kinds = nil
	}
	r.Kinds = kinds
	return r
}

func hasAny(t string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(t, w) {
			return true
		}
	}
	return false
}

func without(list []string, v string) []string {
	var out []string
	for _, x := range list {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
