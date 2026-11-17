package media

import (
	"strings"
	"testing"
)

func TestLogInput(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:1/api/addon-input/ab/0?k=secret":       "http://127.0.0.1:1/api/addon-input/ab/0?k=…",
		"http://127.0.0.1:1/api/addon-input/ab/0?a=1&k=secret&b": "http://127.0.0.1:1/api/addon-input/ab/0?a=1&k=…&b",
		"0?k=secret":                    "0?k=…",
		"/media/movies/Film (1999).mkv": "/media/movies/Film (1999).mkv",
		"ffmpeg: http://127.0.0.1:1/x?k=secret: Server returned 404": "ffmpeg: http://127.0.0.1:1/x?k=…: Server returned 404",
	} {
		got := logInput(in)
		if got != want || strings.Contains(got, "secret") {
			t.Errorf("logInput(%q) = %q, want %q", in, got, want)
		}
	}
}
