package config

import "testing"

func TestJellyfinDefaults(t *testing.T) {
	t.Setenv("VAULT_JELLYFIN_LISTEN", "")
	t.Setenv("VAULT_JELLYFIN_VERSION", "")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.JellyfinListen != ":8096" || c.JellyfinVersion != "12.1.0" {
		t.Fatalf("%q %q", c.JellyfinListen, c.JellyfinVersion)
	}
	t.Setenv("VAULT_JELLYFIN_LISTEN", "off")
	t.Setenv("VAULT_JELLYFIN_VERSION", "10.11.0")
	if c, _ = Load(); c.JellyfinListen != "off" || c.JellyfinVersion != "10.11.0" {
		t.Fatalf("%q %q", c.JellyfinListen, c.JellyfinVersion)
	}
}
