package iptv

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestSealRoundTrip(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	s, err := NewSealer(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	plain := "http://u:p@example.test/get.php?username=u&password=p"
	a, err := s.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := s.Seal(plain)
	if bytes.Equal(a, b) {
		t.Fatal("same nonce twice")
	}
	if bytes.Contains(a, []byte("password")) {
		t.Fatal("sealed text carries the plain text")
	}
	got, err := s.Open(a)
	if err != nil || got != plain {
		t.Fatalf("open: %q %v", got, err)
	}
	// The same key comes back from the database.
	s2, err := NewSealer(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s2.Open(b); err != nil || got != plain {
		t.Fatalf("second sealer: %q %v", got, err)
	}
	a[len(a)-1] ^= 1
	if _, err := s.Open(a); err == nil {
		t.Fatal("tampered text opened")
	}
	if got, err := s.Open(nil); err != nil || got != "" {
		t.Fatalf("open nil: %q %v", got, err)
	}
}

func TestMaskHidesEverythingButHost(t *testing.T) {
	for in, want := range map[string]string{
		"http://u:p@example.test:8080/get.php?username=u&password=p": "http://example.test:8080/…***",
		"https://example.test/list.m3u":                              "https://example.test/…***",
		"not a url at all":                                           "***",
		"":                                                           "",
	} {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripCreds(t *testing.T) {
	for in, want := range map[string]string{
		"http://example.test:8080/live/u/p/123.ts":                           "http://example.test:8080/live/123.ts",
		"http://example.test/movie/u/p/77.mkv":                               "http://example.test/movie/77.mkv",
		"http://u:p@example.test/a.m3u8?token=abc&q=1&Password=x&quality=hd": "http://example.test/a.m3u8?q=1&quality=hd",
		"http://example.test/get.php?username=u&password=p&type=m3u_plus":    "http://example.test/get.php?type=m3u_plus",
		"https://example.test/plain/stream.m3u8":                             "https://example.test/plain/stream.m3u8",
	} {
		got := StripCreds(in)
		if got != want {
			t.Errorf("StripCreds(%q) = %q, want %q", in, got, want)
		}
		for _, bad := range []string{"u:p", "password", "token", "/u/p/"} {
			if strings.Contains(strings.ToLower(got), bad) {
				t.Errorf("StripCreds(%q) = %q keeps %q", in, got, bad)
			}
		}
	}
	if got := StripCreds("::not a url with password=x"); strings.Contains(got, "password") {
		t.Errorf("unparseable: %q", got)
	}
}
