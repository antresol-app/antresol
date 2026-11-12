package channels

import (
	"strings"

	"github.com/antresol-app/antresol/internal/subtitles"
)

// iso639_2 are the ISO 639-2 codes subtitles.NormLang does not know, by
// their ISO 639-1 code (a stream tagged "hun" is "hu").
var iso639_2 = map[string]string{
	"tur": "tr", "hun": "hu", "swe": "sv", "nor": "no", "nob": "nb", "nno": "nn", "dan": "da", "fin": "fi",
	"dut": "nl", "nld": "nl", "cze": "cs", "ces": "cs", "slo": "sk", "slk": "sk", "gre": "el", "ell": "el",
	"heb": "he", "ara": "ar", "hin": "hi", "tha": "th", "vie": "vi", "kaz": "kk", "bel": "be", "geo": "ka",
	"kat": "ka", "arm": "hy", "hye": "hy", "rum": "ro", "ron": "ro", "bul": "bg", "srp": "sr", "hrv": "hr",
	"lit": "lt", "lav": "lv", "est": "et", "per": "fa", "fas": "fa", "ind": "id", "may": "ms", "msa": "ms",
	"aze": "az", "uzb": "uz", "slv": "sl", "ice": "is", "isl": "is", "cat": "ca", "baq": "eu", "eus": "eu",
	"glg": "gl", "tgl": "tl", "fil": "tl", "ben": "bn", "tam": "ta", "tel": "te", "urd": "ur", "mon": "mn",
}

// langTag is a stream's language tag as ISO 639-1 ("rus" → "ru", "hun" →
// "hu"), else the lower-cased tag when it looks like a code ("tlh"),
// else "" (unknown, "und").
func langTag(tag string) string {
	if c := subtitles.NormLang(tag); c != "" {
		return c
	}
	t := strings.ToLower(strings.TrimSpace(tag))
	if c := iso639_2[t]; c != "" {
		return c
	}
	if t != "" && t != "und" && t != "mul" && t != "zxx" && len(t) <= 3 {
		return t
	}
	return ""
}

// bcp47 is the HLS LANGUAGE of an ISO 639 code ("" → "und").
func bcp47(lang string) string {
	if lang == "" {
		return "und"
	}
	return lang
}

// endonyms are the languages' own names: a rendition of another language
// is named so («English», «Français»), whatever the server's language.
var endonyms = map[string]string{
	"ru": "Русский", "en": "English", "fr": "Français", "de": "Deutsch", "es": "Español", "it": "Italiano",
	"pt": "Português", "ja": "日本語", "zh": "中文", "ko": "한국어", "uk": "Українська", "pl": "Polski",
	"tr": "Türkçe", "hu": "Magyar", "sv": "Svenska", "no": "Norsk", "nb": "Norsk bokmål", "nn": "Nynorsk",
	"da": "Dansk", "fi": "Suomi", "nl": "Nederlands", "cs": "Čeština", "sk": "Slovenčina", "el": "Ελληνικά",
	"he": "עברית", "ar": "العربية", "hi": "हिन्दी", "th": "ไทย", "vi": "Tiếng Việt", "kk": "Қазақ тілі",
	"be": "Беларуская", "ka": "ქართული", "hy": "Հայերեն", "ro": "Română", "bg": "Български", "sr": "Српски",
	"hr": "Hrvatski", "lt": "Lietuvių", "lv": "Latviešu", "et": "Eesti", "fa": "فارسی", "id": "Bahasa Indonesia",
	"ms": "Bahasa Melayu", "az": "Azərbaycan", "uz": "Oʻzbek", "sl": "Slovenščina", "is": "Íslenska",
	"ca": "Català", "eu": "Euskara", "gl": "Galego", "tl": "Filipino", "bn": "বাংলা", "ta": "தமிழ்",
	"te": "తెలుగు", "ur": "اردو", "mn": "Монгол",
}

// endonym is the language's own name, else its code in capitals ("" for
// an unknown language).
func endonym(lang string) string {
	if n := endonyms[lang]; n != "" {
		return n
	}
	return strings.ToUpper(lang)
}
