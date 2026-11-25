package api

import (
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/antresol-app/antresol/internal/netguard"
)

// Where the relay may dial: netguard's rules, by the playlist address the
// admin typed for the source (sourceOnLAN picks the client).

// relayClient: lan false dials public addresses only; true public ones and
// the LAN's. Each has its own connection pool, so a connection opened for
// one is never reused by the other.
func relayClient(lan bool) *http.Client {
	d := &net.Dialer{Timeout: relayConnect, KeepAlive: 30 * time.Second, ControlContext: netguard.Control(lan)}
	tr := &http.Transport{
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   relayConnect,
		ResponseHeaderTimeout: relayHeaders,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	return &http.Client{Transport: tr, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("relay: too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errRelayRef
		}
		return nil
	}}
}
