package wikidata

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientSwitchedOff: «Внешние сервисы» off sends nothing (ErrOff), on
// again asks at once.
func TestClientSwitchedOff(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"search":[]}`))
	}))
	defer srv.Close()
	on := false
	c := &Client{BaseURL: srv.URL, Enabled: func() bool { return on }}
	if c.Active() {
		t.Fatal("active while off")
	}
	if _, err := c.SearchBooks(t.Context(), "Шантарам", "ru"); !errors.Is(err, ErrOff) || hits != 0 {
		t.Fatalf("off: %v, %d requests", err, hits)
	}
	if _, _, _, err := c.Raw(t.Context(), "/w/api.php", nil); !errors.Is(err, ErrOff) {
		t.Fatalf("raw off: %v", err)
	}
	on = true
	if _, err := c.SearchBooks(t.Context(), "Шантарам", "ru"); err != nil || hits == 0 {
		t.Fatalf("on: %v, %d requests", err, hits)
	}
	var none *Client
	if none.Active() {
		t.Fatal("nil client active")
	}
}
