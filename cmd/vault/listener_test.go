package main

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// A Jellyfin port that is taken (a Jellyfin still
// running) is logged and left off; it never reports an error that would
// stop Vault. The main listener's failure still does.
func TestOptionalListenerFailureDoesNotStopVault(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	errc := make(chan error, 2)
	done := make(chan struct{})
	go func() { runListener(&http.Server{Addr: busy.Addr().String()}, true, errc); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the optional listener did not give up")
	}
	select {
	case err := <-errc:
		t.Fatalf("the optional listener reported %v", err)
	default:
	}
	go runListener(&http.Server{Addr: busy.Addr().String()}, false, errc)
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("no error from the main listener")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the main listener's failure was not reported")
	}
}
