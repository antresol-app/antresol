package iptv

import (
	"strings"
	"unicode"
)

// A source's language filter: a playlist of thousands of channels carries
// dozens of languages; Source.Langs keeps the wanted ones and hides a
// channel known to be in another (WhyLanguage). A channel's language is
// judged by what the playlist says first — a language or country mark in
// its group-title or name («UA | Новости», «[DE] Sport»), its tvg-id's
// country («bbc.uk»), letters only one language uses (Ukrainian «і ї є ґ»)
// — then the AI helper's answer (categories.go); a channel nobody can
// place stays shown.

// WhyLanguage: hidden by the source's language filter (a catalog key).
const WhyLanguage = "пресет: другой язык"

// langMarks: words of group-titles and names that say a language (lower
// case, whole words): ISO codes that providers use for countries, and the
// countries' and languages' names.
var langMarks = map[string]string{
	"ru": "ru", "rus": "ru", "russia": "ru", "russian": "ru", "россия": "ru", "русские": "ru", "русский": "ru", "рф": "ru",
	"ua": "uk", "ukr": "uk", "ukraine": "uk", "ukrainian": "uk", "украина": "uk", "украинские": "uk", "україна": "uk",
	"by": "be", "belarus": "be", "беларусь": "be", "белоруссия": "be", "белорусские": "be",
	"en": "en", "eng": "en", "english": "en", "uk": "en", "gb": "en", "usa": "en", "us": "en", "англия": "en", "сша": "en", "английские": "en",
	"de": "de", "ger": "de", "germany": "de", "german": "de", "deutsch": "de", "германия": "de", "немецкие": "de",
	"fr": "fr", "fra": "fr", "france": "fr", "french": "fr", "франция": "fr",
	"es": "es", "spa": "es", "spain": "es", "spanish": "es", "испания": "es",
	"it": "it", "ita": "it", "italy": "it", "italian": "it", "италия": "it",
	"pl": "pl", "pol": "pl", "poland": "pl", "polska": "pl", "польша": "pl",
	"tr": "tr", "tur": "tr", "turkey": "tr", "turkish": "tr", "türkiye": "tr", "турция": "tr", "турецкие": "tr",
	"ar": "ar", "arabic": "ar", "arab": "ar", "арабские": "ar",
	"am": "hy", "arm": "hy", "armenia": "hy", "армения": "hy", "армянские": "hy",
	"ge": "ka", "geo": "ka", "georgia": "ka", "грузия": "ka", "грузинские": "ka",
	"az": "az", "aze": "az", "azerbaijan": "az", "азербайджан": "az",
	"uz": "uz", "uzb": "uz", "uzbekistan": "uz", "узбекистан": "uz",
	"il": "he", "israel": "he", "израиль": "he",
	"pt": "pt", "portugal": "pt", "brazil": "pt", "португалия": "pt",
	"in": "hi", "india": "hi", "hindi": "hi", "индия": "hi",
	"cn": "zh", "china": "zh", "chinese": "zh", "китай": "zh",
	"ro": "ro", "romania": "ro", "md": "ro", "moldova": "ro", "молдова": "ro",
	"nl": "nl", "netherlands": "nl", "dutch": "nl",
	"gr": "el", "greece": "el", "greek": "el",
	"bg": "bg", "bulgaria": "bg", "болгария": "bg",
	"rs": "sr", "serbia": "sr", "сербия": "sr",
	"lt": "lt", "lithuania": "lt", "lv": "lv", "latvia": "lv", "ee": "et", "estonia": "et",
}

// markOf: the language a word says. A code (two or three letters) is
// too often something else («Movies in HD», «AR TV», «De Niro») and says it
// only where a provider puts a country: the first word, in brackets or
// before a bar (code); a country's or language's name says it anywhere.
func markOf(word string, code bool) string {
	if len([]rune(word)) <= 3 && !code {
		return ""
	}
	return langMarks[word]
}

// tvgCountry: the language of a tvg-id's country suffix («bbc.uk» →
// en). Only the countries with one language of television; kz, by, md
// and the like broadcast in two and say nothing.
var tvgCountry = map[string]string{
	"ru": "ru", "ua": "uk", "uk": "en", "us": "en", "gb": "en", "de": "de", "at": "de", "fr": "fr", "es": "es",
	"it": "it", "pl": "pl", "tr": "tr", "am": "hy", "ge": "ka", "az": "az", "il": "he", "pt": "pt", "br": "pt",
	"nl": "nl", "gr": "el", "bg": "bg", "rs": "sr", "ro": "ro", "lt": "lt", "lv": "lv", "ee": "et", "cn": "zh",
}

// marked: a language mark among words: a code first, in brackets or
// before a bar; a name anywhere (names: only codes and only so — a channel
// called «Russia Today» speaks English).
func marked(text string, names bool) string {
	ws := variantWords(text)
	bar := strings.Index(text, "|")
	for i, w := range ws {
		code := i == 0 || ws[i-1] == "(" || ws[i-1] == "["
		if i == 0 && bar < 0 && !names {
			code = false // a name's first word is its name («Ar Rahma»), not a mark
		}
		if l := markOf(w, code); l != "" && (names || code) {
			return l
		}
	}
	if before, _, ok := strings.Cut(text, "|"); ok {
		if l := markOf(strings.ToLower(strings.TrimSpace(before)), true); l != "" {
			return l
		}
	}
	return ""
}

func groupLang(group string) string { return marked(group, true) }
func nameLang(name string) string   { return marked(name, false) }

// tvgLang: a tvg-id's country suffix («name.ua»).
func tvgLang(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	i := strings.LastIndexAny(id, ".@")
	if i < 0 {
		return ""
	}
	return tvgCountry[id[i+1:]]
}

// scriptLang: letters only Ukrainian (і ї є ґ) or only Belarusian (ў)
// uses among Cyrillic ones.
func scriptLang(name string) string {
	for _, r := range strings.ToLower(name) {
		switch r {
		case 'ї', 'є', 'ґ':
			return "uk"
		case 'ў':
			return "be"
		case 'і':
			if strings.ContainsFunc(name, func(r rune) bool { return unicode.Is(unicode.Cyrillic, r) && r != 'і' && r != 'І' }) {
				return "uk" // і among Cyrillic letters (a Latin i is not)
			}
		}
	}
	return ""
}

// EntryLang is a channel's language by its playlist entry ("" unknown):
// the group-title's mark, the name's, the tvg-id's country, its letters;
// ai: the AI helper's answer, the last word.
func EntryLang(name, group, tvgID, ai string) string {
	for _, l := range []string{groupLang(group), nameLang(name), tvgLang(tvgID), scriptLang(name)} {
		if l != "" {
			return l
		}
	}
	return ai
}
