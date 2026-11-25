package jellyfin

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A client that sends the headers of the login and then drips the body is
// answered (400), not held for ever: the login is open to anyone.
func TestSlowLoginBodyIsCutOff(t *testing.T) {
	old := loginBodyWait
	loginBodyWait = 300 * time.Millisecond
	defer func() { loginBodyWait = old }()
	e := newEnv(t)
	srv := httptest.NewServer(e.server())
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("POST /Users/AuthenticateByName HTTP/1.1\r\nHost: x\r\nContent-Type: application/json\r\nContent-Length: 100\r\n\r\n{\"Username\"")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no answer to a stalled login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || time.Since(start) > 3*time.Second {
		t.Fatalf("stalled login: %d after %v", resp.StatusCode, time.Since(start))
	}
}
