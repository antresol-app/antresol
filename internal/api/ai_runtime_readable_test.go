package api

import "testing"

func TestReadableError(t *testing.T) {
	for in, want := range map[string]string{
		`401 Unauthorized {"error":{"message":"Invalid API Key"}}`: "401 Unauthorized · Invalid API Key",
		`400 Bad Request: {"error":"model not found"}`:             "400 Bad Request · model not found",
		`{"message":"quota"}`:                                      "quota",
		`timeout after 30s`:                                        "timeout after 30s",
		`500 {not json`:                                            `500 {not json`,
	} {
		if got := readableError(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
