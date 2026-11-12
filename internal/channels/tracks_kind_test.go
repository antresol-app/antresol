package channels

import "testing"

func TestVoiceKindInViewerLanguage(t *testing.T) {
	for _, c := range []struct{ lang, in, want string }{
		{"en", "Дубляж", "Dubbed"}, {"en", "многоголосый", "Multi-voice"}, {"ru", "Дубляж", "Дубляж"},
		{"ru", "многоголосый", "Многоголосый"}, {"en", "LostFilm", "LostFilm"},
	} {
		if got := voiceKind(c.lang, c.in); got != c.want {
			t.Errorf("voiceKind(%s, %s) = %s, want %s", c.lang, c.in, got, c.want)
		}
	}
}
