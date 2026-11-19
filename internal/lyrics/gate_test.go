package lyrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestClientSwitchedOff: off finds nothing and asks nothing, as a nil
// client.
func TestClientSwitchedOff(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"plainLyrics":"la"}`))
	}))
	defer srv.Close()
	on := false
	c := NewAt(srv.URL, "test")
	c.Enabled = func() bool { return on }
	if r, err := c.Find(t.Context(), Query{Artist: "A", Title: "B"}); r != nil || err != nil || hits != 0 || c.Active() {
		t.Fatalf("off: %v %v %d", r, err, hits)
	}
	on = true
	if !c.Active() {
		t.Fatal("on: inactive")
	}
	if _, err := c.Find(t.Context(), Query{Artist: "A", Title: "B"}); err != nil || hits == 0 {
		t.Fatalf("on: %v %d", err, hits)
	}
}
