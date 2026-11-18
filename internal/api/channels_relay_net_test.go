package api

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"testing"
)

func TestRelayClassify(t *testing.T) {
	for want, ips := range map[addrClass][]string{
		addrPublic: {"8.8.8.8", "1.1.1.1", "93.184.216.34", "172.15.255.255", "172.32.0.1", "100.63.255.255", "100.128.0.1", "198.17.255.255", "198.20.0.1",
			"223.255.255.255", "2606:4700:4700::1111", "2a00:1450:4001::1", "2001:200::1"},
		addrLAN: {"127.0.0.1", "127.255.255.254", "10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.50.20", "100.64.0.1", "100.127.255.255",
			"::1", "fd00::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:192.168.1.5", "::ffff:10.1.2.3"},
		addrDenied: {"0.0.0.0", "0.1.2.3", "0.255.255.255", "169.254.169.254", "169.254.0.1", "192.0.0.1", "192.0.2.1", "192.88.99.1", "198.18.0.1", "198.19.255.255",
			"198.51.100.7", "203.0.113.9", "224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
			"::", "::2", "::7f00:1", "64:ff9b::7f00:1", "64:ff9b::a00:1", "64:ff9b:1::1", "100::1", "2001::1", "2001:db8::1", "2002:7f00:1::1", "2002:c0a8:101::",
			"fe80::1", "fe80::1%eth0", "fec0::1", "ff02::1", "ff00::", "4000::1", "::ffff:0:7f00:1", "::ffff:169.254.169.254", "::ffff:0.0.0.0", "::ffff:224.0.0.1"},
	} {
		for _, s := range ips {
			ip, err := netip.ParseAddr(s)
			if err != nil {
				t.Fatalf("%s: %v", s, err)
			}
			if got := classify(ip); got != want {
				t.Errorf("classify(%s) = %d, want %d", s, got, want)
			}
		}
	}
	if classify(netip.Addr{}) != addrDenied {
		t.Error("the zero address is not denied")
	}
}

func TestRelayLANSpelling(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "192.168.1.50": true, "10.1.2.3": true, "::1": true, "fd00::5": true, "localhost": true, "LOCALHOST.": true,
		"iptv": true, "playlist-proxy": true, "tv.local": true, "nas.lan": true, "box.home.arpa": true, "x.internal": true, "a.b.localhost": true,
		"": false, "8.8.8.8": false, "provider.example.com": false, "iptv.example.test": false, "localhost.example.com": false,
		"lan.example.com": false, "169.254.169.254": false, "0.0.0.0": false, "2606:4700:4700::1111": false,
		"example.lan.evil.com": false, "internal.example.com": false,
	} {
		if got := lanSpelling(host); got != want {
			t.Errorf("lanSpelling(%q) = %v, want %v", host, got, want)
		}
	}
}

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
