package goodreads

import "testing"

func TestParseProfile(t *testing.T) {
	for in, want := range map[string]struct {
		id   int64
		name string
	}{
		"12345":           {12345, "12345"},
		"12345-some-name": {12345, "some-name"},
		" https://www.goodreads.com/user/show/12345-some-name ":            {12345, "some-name"},
		"goodreads.com/user/show/12345":                                    {12345, "12345"},
		"https://www.goodreads.com/review/list/12345-some-name?shelf=read": {12345, "some-name"},
		// the retired RSS address a profile may still have saved
		"https://www.goodreads.com/review/list_rss/12345?key=abc&shelf=read": {12345, "12345"},
		"https://www.goodreads.com/user_status/list/12345-some-name":         {12345, "some-name"},
	} {
		id, name, ok := ParseProfile(in)
		if !ok || id != want.id || name != want.name {
			t.Errorf("%q: %d %q %v", in, id, name, ok)
		}
	}
	for _, in := range []string{"", "abc", "some-name", "0", "https://example.com/user/show/12345",
		"https://goodreads.com.evil.example/user/show/12345", "https://www.goodreads.com/book/show/12345",
		"https://www.goodreads.com/username", "1234567890123"} {
		if id, _, ok := ParseProfile(in); ok {
			t.Errorf("%q parsed as %d", in, id)
		}
	}
}
