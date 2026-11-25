package subsonic

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A client that sends the headers of a form POST and then drips the body is
// answered, not held for ever: the form is read before anyone signs in.
func TestSlowFormIsCutOff(t *testing.T) {
	old := formWait
	formWait = 300 * time.Millisecond
	defer func() { formWait = old }()
	e := newEnv(t)
	srv := httptest.NewServer(e.srv)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("POST /rest/ping.view HTTP/1.1\r\nHost: x\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 100\r\n\r\nu=")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no answer to a stalled form: %v", err)
	}
	resp.Body.Close()
	if time.Since(start) > 3*time.Second {
		t.Fatalf("answered after %v", time.Since(start))
	}
}
