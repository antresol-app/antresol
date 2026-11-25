package addonkit

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestClassifyAddr(t *testing.T) {
	for want, ips := range map[AddrClass][]string{
		AddrPublic: {"8.8.8.8", "1.1.1.1", "93.184.216.34", "172.15.255.255", "172.32.0.1", "100.63.255.255", "100.128.0.1", "198.17.255.255", "198.20.0.1",
			"223.255.255.255", "2606:4700:4700::1111", "2606:2800:220:1::1", "2a00:1450:4001::1", "2001:200::1",
			// NAT64 and 6to4 carrying a public IPv4 address
			"64:ff9b::808:808", "64:ff9b::5db8:d822", "2002:5db8:d822::1"},
		AddrLAN: {"127.0.0.1", "127.255.255.254", "10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.50.20", "100.64.0.1", "100.127.255.255",
			"::1", "fd00::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:192.168.1.5", "::ffff:10.1.2.3"},
		AddrDenied: {"0.0.0.0", "0.1.2.3", "0.255.255.255", "169.254.169.254", "169.254.0.1", "192.0.0.1", "192.0.0.8", "192.0.2.1", "192.88.99.1", "198.18.0.1", "198.19.255.255",
			"198.51.100.7", "203.0.113.9", "224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
			"::", "::2", "::7f00:1", "::127.0.0.1", "64:ff9b::7f00:1", "64:ff9b::a00:1", "64:ff9b::a9fe:a9fe", "64:ff9b:1::1", "64:ff9b:1::c0a8:101",
			"100::1", "2001::1", "2001:db8::1", "2002:7f00:1::1", "2002:c0a8:101::", "2002:c0a8:101::1",
			"fe80::1", "fe80::1%eth0", "fec0::1", "ff02::1", "ff00::", "4000::1", "::ffff:0:7f00:1", "::ffff:169.254.169.254", "::ffff:0.0.0.0", "::ffff:224.0.0.1"},
	} {
		for _, s := range ips {
			ip, err := netip.ParseAddr(s)
			if err != nil {
				t.Fatalf("%s: %v", s, err)
			}
			if got := ClassifyAddr(ip); got != want {
				t.Errorf("ClassifyAddr(%s) = %d, want %d", s, got, want)
			}
		}
	}
	if ClassifyAddr(netip.Addr{}) != AddrDenied {
		t.Error("the zero address is not denied")
	}
}

func TestBlockedAddr(t *testing.T) {
	for _, a := range []string{"127.0.0.1", "10.0.0.5", "192.168.100.100", "172.16.0.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "224.0.0.1",
		"::1", "[::1]", "fe80::1", "fc00::1", "fd00::1", "::ffff:127.0.0.1", "64:ff9b::a00:1", "2002:7f00:1::1", "192.0.0.8", "198.18.0.1", "240.0.0.1",
		"not-an-ip", ""} {
		if !BlockedAddr(a) {
			t.Errorf("%q must be blocked", a)
		}
	}
	for _, a := range []string{"93.184.216.34", "8.8.8.8", "2606:4700:4700::1111", "64:ff9b::808:808"} {
		if BlockedAddr(a) {
			t.Errorf("%q must be allowed", a)
		}
	}
}

func TestDialControl(t *testing.T) {
	for addr, want := range map[string][2]bool{ // public-only dialer, the LAN's too: allowed
		"8.8.8.8:80":                  {true, true},
		"[2606:4700:4700::1111]:443":  {true, true},
		"127.0.0.1:80":                {false, true},
		"[::1]:80":                    {false, true},
		"192.168.1.2:80":              {false, true},
		"100.64.0.9:80":               {false, true},
		"[64:ff9b::a00:1]:443":        {false, false},
		"169.254.169.254:80":          {false, false},
		"[::ffff:169.254.169.254]:80": {false, false},
		"[fe80::1%eth0]:80":           {false, false},
		"0.0.0.0:80":                  {false, false},
		"224.0.0.1:80":                {false, false},
		"[ff02::1]:80":                {false, false},
		"not an address":              {false, false},
	} {
		for i, allowPrivate := range []bool{false, true} {
			err := DialControl(allowPrivate)("tcp", addr, nil)
			if (err == nil) != want[i] || (err != nil && !errors.Is(err, ErrBlocked)) {
				t.Errorf("DialControl(%v)(%s) = %v, want allowed %v", allowPrivate, addr, err, want[i])
			}
		}
	}
}

func TestGuardedTransport(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		io.WriteString(w, "ok")
	}))
	defer srv.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL, http.StatusFound)
	}))
	defer redirect.Close()

	get := func(allowPrivate bool, u string) error {
		c := &http.Client{Transport: NewGuardedTransport(allowPrivate), Timeout: 3 * time.Second}
		resp, err := c.Get(u)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}
	if err := get(false, srv.URL); !errors.Is(err, ErrBlocked) {
		t.Fatalf("loopback, public only: %v, want ErrBlocked", err)
	}
	if hits != 0 {
		t.Fatalf("the loopback server saw %d requests", hits)
	}
	if err := get(true, srv.URL); err != nil || hits != 1 {
		t.Fatalf("loopback, LAN allowed: %v (%d requests)", err, hits)
	}
	if err := get(true, redirect.URL); err != nil || hits != 2 {
		t.Fatalf("a redirect inside the LAN, LAN allowed: %v (%d requests)", err, hits)
	}
	// metadata and link-local stay out of reach whatever the admin allowed
	if err := get(true, "http://169.254.169.254/latest"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("link-local, LAN allowed: %v, want ErrBlocked", err)
	}
	if tr := NewGuardedTransport(false); tr.Proxy != nil || !tr.ForceAttemptHTTP2 {
		t.Fatalf("proxy %v, HTTP/2 %v", tr.Proxy != nil, tr.ForceAttemptHTTP2)
	}
}
