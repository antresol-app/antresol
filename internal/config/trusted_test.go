package config

import "testing"

func TestTrustedNetsAndProxies(t *testing.T) {
	t.Setenv("VAULT_HWACCEL", "")
	t.Setenv("VAULT_TRUSTED_NETS", "100.64.0.0/10, 2001:db8::/32")
	t.Setenv("VAULT_TRUSTED_PROXIES", "192.168.1.2")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.TrustedNets) != 2 || c.TrustedNets[0].String() != "100.64.0.0/10" || c.TrustedNets[1].String() != "2001:db8::/32" {
		t.Fatalf("nets %v", c.TrustedNets)
	}
	if len(c.TrustedProxies) != 1 || c.TrustedProxies[0].String() != "192.168.1.2/32" {
		t.Fatalf("proxies %v", c.TrustedProxies)
	}
	t.Setenv("VAULT_TRUSTED_PROXIES", "npm.local")
	if _, err := Load(); err == nil {
		t.Fatal("bad proxy accepted")
	}
	t.Setenv("VAULT_TRUSTED_PROXIES", "")
	t.Setenv("VAULT_TRUSTED_NETS", "")
	c, _ = Load()
	if len(c.TrustedNets) != 0 || len(c.TrustedProxies) != 0 {
		t.Fatalf("defaults should be empty: %v %v", c.TrustedNets, c.TrustedProxies)
	}
}
