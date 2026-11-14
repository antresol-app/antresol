package config

import "testing"

func TestDiscoveryDefaults(t *testing.T) {
	t.Setenv("VAULT_MEDIA_ROOT", "/srv/media")
	for _, k := range []string{"VAULT_DISCOVERY", "VAULT_MDNS", "VAULT_DISCOVERY_PORT", "VAULT_SERVER_NAME"} {
		t.Setenv(k, "")
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.MDNS || c.DiscoveryPort != 8420 || c.ServerName != "Antresol" {
		t.Fatalf("%v %d %q", c.MDNS, c.DiscoveryPort, c.ServerName)
	}
}

func TestDiscoverySwitches(t *testing.T) {
	t.Setenv("VAULT_MEDIA_ROOT", "/srv/media")
	t.Setenv("VAULT_MDNS", "off")
	t.Setenv("VAULT_DISCOVERY_PORT", "7400")
	t.Setenv("VAULT_SERVER_NAME", " Дача ")
	c, err := Load()
	if err != nil || c.MDNS || c.DiscoveryPort != 7400 || c.ServerName != "Дача" {
		t.Fatalf("%+v %v", c, err)
	}
	t.Setenv("VAULT_DISCOVERY", "off")
	if c, err = Load(); err != nil || c.MDNS || c.DiscoveryPort != 0 {
		t.Fatalf("all off: %v %d %v", c.MDNS, c.DiscoveryPort, err)
	}
	t.Setenv("VAULT_DISCOVERY", "")
	for _, bad := range []string{"70000", "port"} {
		t.Setenv("VAULT_DISCOVERY_PORT", bad)
		if _, err := Load(); err == nil {
			t.Errorf("VAULT_DISCOVERY_PORT=%s accepted", bad)
		}
	}
	t.Setenv("VAULT_DISCOVERY_PORT", "off")
	t.Setenv("VAULT_MDNS", "maybe")
	if _, err := Load(); err == nil {
		t.Error("VAULT_MDNS=maybe accepted")
	}
}
