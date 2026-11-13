package config

import (
	"strings"
	"testing"
)

func TestPathHintsHostPathInContainer(t *testing.T) {
	c := Config{MediaRoot: "/tank/media", DataDir: "/config", MoviesDir: "moovies", SeriesDir: "/media/series",
		TranscodeDir: "/tank/tmp/antresol-hls"}
	exists := func(p string) bool { return p == "/" || p == "/media" || p == "/media/series" || p == "/config" }
	got := c.PathHints(exists)
	if len(got) != 1 {
		t.Fatalf("want one hint for the /tank root, got %q", got)
	}
	h := got[0]
	for _, want := range []string{"/tank", "VAULT_MEDIA_ROOT", "VAULT_MOVIES_DIR", "VAULT_TRANSCODE_DIR", "inside the container"} {
		if !strings.Contains(h, want) {
			t.Fatalf("hint %q lacks %q", h, want)
		}
	}
	if strings.Contains(h, "VAULT_SERIES_DIR") {
		t.Fatalf("series exists, must not be named: %q", h)
	}
}

func TestPathHintsQuietWhenRootExists(t *testing.T) {
	// a missing folder under an existing root is a plain scan error, not a hint
	c := Config{MediaRoot: "/media", MoviesDir: "movies", TranscodeDir: "/media/tmp/hls"}
	if got := c.PathHints(func(p string) bool { return p == "/" || p == "/media" }); len(got) != 0 {
		t.Fatalf("unexpected hints %q", got)
	}
}
