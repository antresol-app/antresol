package api

import (
	"strings"
	"testing"
)

// TestKindleAddressIsTheProfilesOwn: every adult sets its own Send to Kindle
// address (PATCH /api/me), checked as the admin's form checks it; a kid's
// stays the admin's.
func TestKindleAddressIsTheProfilesOwn(t *testing.T) {
	e := newLibEnv(t)
	_, mom := e.profile(`{"name":"Мама"}`)
	_, kid := e.profile(`{"name":"Вася","isKid":true,"maxRating":"12+"}`)
	if w := e.do("PATCH", "/api/me", `{"kindleEmail":" mama_42@kindle.com "}`, mom); w.Code != 200 || !strings.Contains(w.Body.String(), `"kindleEmail":"mama_42@kindle.com"`) {
		t.Fatalf("own address: %d %s", w.Code, w.Body)
	}
	if w := e.do("PATCH", "/api/me", `{"kindleEmail":"mama@gmail.com"}`, mom); w.Code != 400 {
		t.Fatalf("not a Kindle address: %d %s", w.Code, w.Body)
	}
	// the other fields stay as they were
	if w := e.do("PATCH", "/api/me", `{"hideProgress":true}`, mom); w.Code != 200 || !strings.Contains(w.Body.String(), `"kindleEmail":"mama_42@kindle.com"`) {
		t.Fatalf("kept: %d %s", w.Code, w.Body)
	}
	if w := e.do("PATCH", "/api/me", `{"kindleEmail":""}`, mom); w.Code != 200 || !strings.Contains(w.Body.String(), `"kindleEmail":""`) {
		t.Fatalf("removed: %d %s", w.Code, w.Body)
	}
	if w := e.do("PATCH", "/api/me", `{"kindleEmail":"vasya@kindle.com"}`, kid); w.Code != 403 {
		t.Fatalf("a kid: %d", w.Code)
	}
}
