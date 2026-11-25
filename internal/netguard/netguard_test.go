package netguard

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestClassify(t *testing.T) {
	for want, ips := range map[Class][]string{
		Public: {"8.8.8.8", "1.1.1.1", "93.184.216.34", "172.15.255.255", "172.32.0.1", "100.63.255.255", "100.128.0.1", "198.17.255.255", "198.20.0.1",
			"223.255.255.255", "2606:4700:4700::1111", "2a00:1450:4001::1", "2001:200::1",
			"64:ff9b::808:808", "2002:5db8:d822::1"}, // NAT64 and 6to4 carrying a public IPv4 address
		LAN: {"127.0.0.1", "127.255.255.254", "10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.50.20", "100.64.0.1", "100.127.255.255",
			"::1", "fd00::1", "fc00::1", "::ffff:127.0.0.1", "::ffff:192.168.1.5", "::ffff:10.1.2.3"},
		Denied: {"0.0.0.0", "0.1.2.3", "0.255.255.255", "169.254.169.254", "169.254.0.1", "192.0.0.1", "192.0.2.1", "192.88.99.1", "198.18.0.1", "198.19.255.255",
			"198.51.100.7", "203.0.113.9", "224.0.0.1", "239.255.255.250", "240.0.0.1", "255.255.255.255",
			"::", "::2", "::7f00:1", "64:ff9b::7f00:1", "64:ff9b::a00:1", "64:ff9b:1::1", "100::1", "2001::1", "2001:db8::1", "2002:7f00:1::1", "2002:c0a8:101::",
			"fe80::1", "fe80::1%eth0", "fec0::1", "ff02::1", "ff00::", "4000::1", "::ffff:0:7f00:1", "::ffff:169.254.169.254", "::ffff:0.0.0.0", "::ffff:224.0.0.1"},
	} {
		for _, s := range ips {
			ip, err := netip.ParseAddr(s)
			if err != nil {
				t.Fatalf("%s: %v", s, err)
			}
			if got := Classify(ip); got != want {
				t.Errorf("Classify(%s) = %d, want %d", s, got, want)
			}
		}
	}
	if Classify(netip.Addr{}) != Denied {
		t.Error("the zero address is not denied")
	}
}

func TestLANSpelling(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "192.168.1.50": true, "10.1.2.3": true, "::1": true, "fd00::5": true, "localhost": true, "LOCALHOST.": true,
		"iptv": true, "playlist-proxy": true, "tv.local": true, "nas.lan": true, "box.home.arpa": true, "x.internal": true, "a.b.localhost": true,
		"": false, "8.8.8.8": false, "provider.example.com": false, "iptv.example.test": false, "localhost.example.com": false,
		"lan.example.com": false, "169.254.169.254": false, "0.0.0.0": false, "2606:4700:4700::1111": false,
		"example.lan.evil.com": false, "internal.example.com": false,
	} {
		if got := LANSpelling(host); got != want {
			t.Errorf("LANSpelling(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestControl(t *testing.T) {
	ctx := context.Background()
	for addr, want := range map[string][2]bool{ // public-only client, LAN client: allowed
		"8.8.8.8:80":                  {true, true},
		"127.0.0.1:80":                {false, true},
		"[::1]:80":                    {false, true},
		"192.168.1.2:80":              {false, true},
		"169.254.169.254:80":          {false, false},
		"[::ffff:169.254.169.254]:80": {false, false},
		"0.0.0.0:80":                  {false, false},
		"not an address":              {false, false},
	} {
		for i, lan := range []bool{false, true} {
			err := Control(lan)(ctx, "tcp", addr, nil)
			if (err == nil) != want[i] || (err != nil && !errors.Is(err, ErrDial)) {
				t.Errorf("Control(%v)(%s) = %v, want allowed %v", lan, addr, err, want[i])
			}
		}
	}
}
