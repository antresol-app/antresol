package api

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A client that sends the headers and then drips the body is answered, not
// held for ever (a slow-body attack on /api/login, open to anyone).
func TestSlowBodyIsCutOff(t *testing.T) {
	old := bodyWait
	bodyWait = 300 * time.Millisecond
	defer func() { bodyWait = old }()
	e := newLibEnv(t)
	srv := httptest.NewServer(e.h)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("POST /api/login HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"userId\"")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no answer to a stalled body: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || time.Since(start) > 3*time.Second {
		t.Fatalf("stalled body: %d after %v", resp.StatusCode, time.Since(start))
	}
}
