package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

func pathRequest(name, value string) *http.Request {
	r := httptest.NewRequest("GET", "/x", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add(name, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rc))
}

func TestPathID(t *testing.T) {
	for _, c := range []struct {
		in   string
		want int64
		ok   bool
	}{{"7", 7, true}, {"0", 0, false}, {"-3", 0, false}, {"x", 0, false}, {"", 0, false}, {"99999999999999999999", 0, false}} {
		if id, ok := pathID(pathRequest("id", c.in), "id"); id != c.want || ok != c.ok {
			t.Errorf("pathID(%q) = %d, %v; want %d, %v", c.in, id, ok, c.want, c.ok)
		}
	}
	w := httptest.NewRecorder()
	if _, ok := idParam(w, pathRequest("id", "0"), "id"); ok || w.Code != http.StatusBadRequest {
		t.Fatalf("idParam(0): ok %v, %d", ok, w.Code)
	}
}

func TestPageParams(t *testing.T) {
	for _, c := range []struct {
		query         string
		limit, offset int
	}{{"", 100, 0}, {"?limit=20&offset=40", 20, 40}, {"?limit=501", 100, 0}, {"?limit=-1&offset=-5", 100, 0}, {"?limit=x&offset=y", 100, 0}} {
		if limit, offset := pageParams(httptest.NewRequest("GET", "/x"+c.query, nil), 100); limit != c.limit || offset != c.offset {
			t.Errorf("pageParams(%q) = %d, %d; want %d, %d", c.query, limit, offset, c.limit, c.offset)
		}
	}
}
