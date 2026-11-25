package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The one body decoder answers what each of the handlers that used to read
// a body by hand answered.
func TestDecodeBody(t *testing.T) {
	const bad = "Не удалось разобрать запрос"
	for _, tc := range []struct {
		name, body string
		max        int64
		tooBig     string
		single     bool
		code       int
		msg        string
	}{
		{"a value", `{"a":1}`, 100, "", false, 200, ""},
		{"a value, trailing white space, strict", `{"a":1}` + " \r\n", 100, "", true, 200, ""},
		{"not JSON", `{"a":`, 100, "", false, 400, bad},
		{"not JSON, with a too-big answer", `{"a":`, 100, "big", false, 400, bad},
		{"too big, no answer for it: a bad request", `{"a":"` + strings.Repeat("x", 200) + `"}`, 100, "", false, 400, bad},
		{"too big", `{"a":"` + strings.Repeat("x", 200) + `"}`, 100, "big", false, 413, "big"},
		{"data after the value is left alone", `{"a":1} {"b":2}`, 100, "", false, 200, ""},
		{"data after the value, strict", `{"a":1} {"b":2}`, 100, "", true, 400, bad},
		{"junk after the value, strict", `{"a":1}x`, 100, "", true, 400, bad},
		{"data after the value, strict, over the limit", `{"a":1}` + strings.Repeat(" ", 200) + `{}`, 100, "big", true, 413, "big"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			var v struct{ A any }
			ok := decodeBody(w, r, &v, tc.max, tc.tooBig, tc.single)
			if tc.code == 200 {
				if !ok || w.Body.Len() != 0 {
					t.Fatalf("ok=%v %d %s", ok, w.Code, w.Body)
				}
				return
			}
			if ok || w.Code != tc.code || !strings.Contains(w.Body.String(), tc.msg) {
				t.Fatalf("ok=%v %d %s, want %d %q", ok, w.Code, w.Body, tc.code, tc.msg)
			}
		})
	}
}
