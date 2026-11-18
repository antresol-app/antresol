package main

import (
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestInputLoopbackBase(t *testing.T) {
	for listen, want := range map[string]string{
		":8420":          "http://127.0.0.1:8420",
		"127.0.0.1:8420": "http://127.0.0.1:8420",
		"0.0.0.0:8420":   "http://127.0.0.1:8420",
		"[::1]:8420":     "http://[::1]:8420",
		"localhost:8420": "http://127.0.0.1:8420",
		"192.0.2.1:8420": "",
		"8420":           "",
		"":               "",
	} {
		if got := inputLoopbackBase(listen); got != want {
			t.Errorf("inputLoopbackBase(%q) = %q, want %q", listen, got, want)
		}
	}
}

func TestNewInputToken(t *testing.T) {
	a, err := newInputToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newInputToken()
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(a) || a == b {
		t.Fatalf("tokens %q %q", a, b)
	}
}

func TestPlaybackRequest_Inputs(t *testing.T) {
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/addon-input/ab/0", true},
		{"HEAD", "/api/addon-input/ab/3", true},
		{"GET", "/api/play/input/ab/direct", true},
		{"POST", "/api/play/input/ab", false},
		{"GET", "/api/play/input/ab/info", false},
		{"PUT", "/api/play/input/ab/progress", false},
		{"GET", "/api/play/input/ab/subs/0.vtt", false},
		{"GET", "/api/play/input/a/b/direct", false},
		{"GET", "/api/play/input//direct", false},
		{"GET", "/api/play/input/direct", false},
	} {
		if got := playbackRequest(httptest.NewRequest(c.method, c.path, nil)); got != c.want {
			t.Errorf("%s %s: %v, want %v", c.method, c.path, got, c.want)
		}
	}
}

func TestRelayPort(t *testing.T) {
	for base, want := range map[string]int{"http://127.0.0.1:8420": 8420, "http://[::1]:8420": 0, "": 0, "http://127.0.0.1:x": 0} {
		if got := relayPort(base); got != want {
			t.Errorf("%q: %d", base, got)
		}
	}
}
