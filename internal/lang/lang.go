// Package lang is the catalog of languages the viewer can prefer
// («Настройки» → «Языки»): ISO 639-1 codes, their Russian names, and the
// checks the settings share.
package lang

import "github.com/antresol-app/antresol/internal/i18n"

// Codes are the languages offered, in the order the settings list them.
var Codes = []string{"ru", "en", "uk", "de", "fr", "es", "it", "pt", "pl", "ja", "zh", "ko"}

// phrases are the languages in the phrase «на …» (a release's reason line);
// each is a catalog key (English: "in Russian").
var phrases = map[string]string{
	"ru": "на русском", "en": "на английском", "uk": "на украинском", "de": "на немецком",
	"fr": "на французском", "es": "на испанском", "it": "на итальянском", "pt": "на португальском",
	"pl": "на польском", "ja": "на японском", "zh": "на китайском", "ko": "на корейском",
}

// names are the languages' names (nominative, capitalized); catalog keys
// (English: "Russian").
var names = map[string]string{
	"ru": "Русский", "en": "Английский", "uk": "Украинский", "de": "Немецкий",
	"fr": "Французский", "es": "Испанский", "it": "Итальянский", "pt": "Португальский",
	"pl": "Польский", "ja": "Японский", "zh": "Китайский", "ko": "Корейский",
}

// Known reports whether code is in the catalog.
func Known(code string) bool { _, ok := phrases[code]; return ok }

// In is «на русском» for "ru"; "" for an unknown code.
func In(code string) string { return InL(i18n.RU, code) }

// InL is In in the interface language ui: "in Russian" for ("en", "ru").
func InL(ui, code string) string {
	if p := phrases[code]; p != "" {
		return i18n.TL(ui, p)
	}
	return ""
}

// Name is «Русский» for "ru"; "" for an unknown code.
func Name(code string) string { return NameL(i18n.RU, code) }

// NameL is Name in the interface language ui: "Russian" for ("en", "ru").
func NameL(ui, code string) string {
	if n := names[code]; n != "" {
		return i18n.TL(ui, n)
	}
	return ""
}

// Valid reports whether l is a list of known codes without repeats, at
// least min long.
func Valid(l []string, min int) bool {
	if len(l) < min || len(l) > len(Codes) {
		return false
	}
	seen := map[string]bool{}
	for _, c := range l {
		if !Known(c) || seen[c] {
			return false
		}
		seen[c] = true
	}
	return true
}

// Within keeps the codes of l that are in allowed, in l's order.
func Within(l, allowed []string) []string {
	ok := map[string]bool{}
	for _, c := range allowed {
		ok[c] = true
	}
	out := []string{}
	for _, c := range l {
		if ok[c] {
			out = append(out, c)
		}
	}
	return out
}

// Ordered puts the codes of l in the order of order (the viewer's
// languages); codes missing from order are dropped.
func Ordered(l, order []string) []string {
	return Within(order, l)
}

// Bonus is the score of a release in the language at rank i of the
// preferred list (first 10, second 6, third 3, later 1); -1 means not
// preferred: 0.
func Bonus(rank int) int {
	switch {
	case rank < 0:
		return 0
	case rank == 0:
		return 10
	case rank == 1:
		return 6
	case rank == 2:
		return 3
	}
	return 1
}

// Rank is the position of code in l, -1 when absent.
func Rank(l []string, code string) int {
	for i, c := range l {
		if c == code {
			return i
		}
	}
	return -1
}
