package api

import "testing"

func TestBrowserOf(t *testing.T) {
	for _, c := range []struct{ ua, browser, system string }{
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1", "Safari", "iPhone"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) CriOS/130.0 Mobile/15E148 Safari/604.1", "Chrome", "iPhone"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Safari/605.1.15", "Safari", "Mac"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36 Edg/130.0", "Edge", "Windows"},
		{"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36", "Chrome", "Android"},
		{"Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0", "Firefox", "Linux"},
		{"curl/8.0", "", ""},
	} {
		if b, s := browserOf(c.ua); b != c.browser || s != c.system {
			t.Errorf("browserOf(%q) = %q, %q; want %q, %q", c.ua, b, s, c.browser, c.system)
		}
	}
}
