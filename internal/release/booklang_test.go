package release

import "testing"

func TestBookLang(t *testing.T) {
	for name, want := range map[string]string{
		"Робертс Грегори Дэвид - Шантарам [2010, FB2]":            "ru",
		"Gregory David Roberts - Shantaram [RUS, FB2]":            "ru",
		"Shantaram (на русском) EPUB":                             "ru",
		"Shantaram, Russian edition":                              "ru",
		"Робертс - Шантарам [ENG, EPUB]":                          "en",
		"Roberts G. - Shantaram (2003) [ENG]":                     "en",
		"Roberts G. - Shantaram (2003) English":                   "en",
		"Робертс Г. - Шантарам [2016, MP3, на английском]":        "en",
		"Gregory David Roberts - Shantaram (2003) EPUB":           "",
		"Gregory David Roberts - Shantaram [stamp-site.com].epub": "",
	} {
		if got := BookLang(name); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
}

func TestLangCode(t *testing.T) {
	for code, want := range map[string]string{"ru": "ru", "RUS": "ru", "ru-RU": "ru", "en_US": "en", "eng": "en", "de": "de", "ger": "de", "pt-BR": "pt", "xx": "", "": ""} {
		if got := LangCode(code); got != want {
			t.Errorf("%q: %q", code, got)
		}
	}
}

// Every tag LangTag writes, ParseBook reads back as the same code.
func TestLangTagRoundTrips(t *testing.T) {
	for code, tag := range langTags {
		if got := BookLang("Author - Title [" + tag + "]"); got != code {
			t.Errorf("%s → %s → %q", code, tag, got)
		}
	}
}
