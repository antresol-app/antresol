package transcript

import "strings"

// languageNames: Whisper's answers name the language in English
// (OpenAI's verbose_json says "russian"); the codes they mean.
var languageNames = map[string]string{
	"afrikaans": "af", "albanian": "sq", "amharic": "am", "arabic": "ar", "armenian": "hy", "azerbaijani": "az",
	"basque": "eu", "belarusian": "be", "bengali": "bn", "bosnian": "bs", "bulgarian": "bg", "catalan": "ca",
	"chinese": "zh", "croatian": "hr", "czech": "cs", "danish": "da", "dutch": "nl", "english": "en", "estonian": "et",
	"finnish": "fi", "french": "fr", "galician": "gl", "georgian": "ka", "german": "de", "greek": "el", "gujarati": "gu",
	"hebrew": "he", "hindi": "hi", "hungarian": "hu", "icelandic": "is", "indonesian": "id", "italian": "it",
	"japanese": "ja", "kannada": "kn", "kazakh": "kk", "khmer": "km", "korean": "ko", "latin": "la", "latvian": "lv",
	"lithuanian": "lt", "macedonian": "mk", "malay": "ms", "malayalam": "ml", "marathi": "mr", "mongolian": "mn",
	"nepali": "ne", "norwegian": "no", "persian": "fa", "polish": "pl", "portuguese": "pt", "punjabi": "pa",
	"romanian": "ro", "russian": "ru", "serbian": "sr", "slovak": "sk", "slovenian": "sl", "spanish": "es",
	"swahili": "sw", "swedish": "sv", "tagalog": "tl", "tajik": "tg", "tamil": "ta", "tatar": "tt", "telugu": "te",
	"thai": "th", "turkish": "tr", "turkmen": "tk", "ukrainian": "uk", "urdu": "ur", "uzbek": "uz",
	"vietnamese": "vi", "welsh": "cy", "yiddish": "yi",
}

// languageCode is the ISO 639-1 code of a language a service names: a
// code, a tag such as ru-RU, or an English name; "" unknown.
func languageCode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if c, ok := languageNames[s]; ok {
		return c
	}
	s, _, _ = strings.Cut(s, "-")
	s, _, _ = strings.Cut(s, "_")
	if len(s) == 2 && s[0] >= 'a' && s[0] <= 'z' && s[1] >= 'a' && s[1] <= 'z' {
		return s
	}
	return ""
}
