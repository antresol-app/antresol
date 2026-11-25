package api

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// The dial itself is checked, so a name that answers a LAN address, a
// redirect, an IPv4-mapped or a metadata address all stop at the socket.
func TestRelayClientsCheckEveryDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	defer srv.Close()
	get := func(c *http.Client, u string) error {
		resp, err := c.Get(u)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	open, lan := relayClient(false), relayClient(true)
	if err := get(open, srv.URL); !errors.Is(err, errRelayDial) {
		t.Errorf("internet client reached loopback: %v", err)
	}
	// a name that resolves to loopback (what a rebinding host does after its first answer)
	if err := get(open, "http://localhost:"+strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port)); !errors.Is(err, errRelayDial) {
		t.Errorf("internet client reached loopback by name: %v", err)
	}
	if err := get(lan, srv.URL); err != nil {
		t.Errorf("LAN client could not reach loopback: %v", err)
	}
	for _, u := range []string{"http://169.254.169.254/", "http://[::ffff:169.254.169.254]/", "http://0.0.0.0:1/", "http://[fe80::1]:1/", "http://224.0.0.1:1/"} {
		for name, c := range map[string]*http.Client{"internet": open, "LAN": lan} {
			if err := get(c, u); !errors.Is(err, errRelayDial) {
				t.Errorf("%s client, %s: %v", name, u, err)
			}
		}
	}
	// a redirect from an allowed address to a refused one
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer redir.Close()
	if err := get(lan, redir.URL); !errors.Is(err, errRelayDial) {
		t.Errorf("redirect to metadata: %v", err)
	}
}
