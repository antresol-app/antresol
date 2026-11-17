package config

import (
	"path/filepath"
	"testing"
)

func loadAddons(t *testing.T, env map[string]string) Config {
	t.Helper()
	for _, k := range []string{"VAULT_ADDONS", "VAULT_ADDONS_DEV", "VAULT_ADDONS_PIN", "VAULT_LISTEN", "VAULT_DATA_DIR"} {
		t.Setenv(k, "")
	}
	t.Setenv("VAULT_MEDIA_ROOT", "/srv/media")
	for k, v := range env {
		t.Setenv(k, v)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAddonsDefaults(t *testing.T) {
	c := loadAddons(t, map[string]string{"VAULT_DATA_DIR": "/var/data"})
	if !c.Addons {
		t.Fatal("addons should be on by default")
	}
	if c.AddonsDir != filepath.Join("/var/data", "addons") {
		t.Fatalf("dir %q", c.AddonsDir)
	}
	if c.AddonsDev != "" {
		t.Fatalf("dev %q", c.AddonsDev)
	}
}

func TestAddonsOff(t *testing.T) {
	if loadAddons(t, map[string]string{"VAULT_ADDONS": "off"}).Addons {
		t.Fatal("VAULT_ADDONS=off should disable addons")
	}
}

func TestAddonsDevPath(t *testing.T) {
	c := loadAddons(t, map[string]string{"VAULT_DATA_DIR": "/var/data", "VAULT_ADDONS_DEV": "dev-addons"})
	if c.AddonsDev != filepath.Join("/var/data", "dev-addons") {
		t.Fatalf("relative dev %q", c.AddonsDev)
	}
	c = loadAddons(t, map[string]string{"VAULT_ADDONS_DEV": "/opt/addons"})
	if c.AddonsDev != "/opt/addons" {
		t.Fatalf("absolute dev %q", c.AddonsDev)
	}
}

func TestAddonsCoreURL(t *testing.T) {
	for listen, want := range map[string]string{
		":8420":              "http://127.0.0.1:8420",
		"0.0.0.0:9000":       "http://127.0.0.1:9000",
		"[::]:8421":          "http://127.0.0.1:8421",
		"192.0.2.5:80":       "http://192.0.2.5:80",
		"127.0.0.1:8420":     "http://127.0.0.1:8420",
		"[2001:db8::1]:8420": "http://[2001:db8::1]:8420",
		"localhost:8420":     "http://localhost:8420",
	} {
		if got := (Config{Listen: listen}).AddonsCoreURL(); got != want {
			t.Errorf("%q: %q, want %q", listen, got, want)
		}
	}
}

func TestAddonsPinEnv(t *testing.T) {
	if c := loadAddons(t, nil); c.AddonsPin != "" {
		t.Fatalf("default pin %q", c.AddonsPin)
	}
	c := loadAddons(t, map[string]string{"VAULT_ADDONS_PIN": " hello@1.0.0,other@2.0.0 "})
	if c.AddonsPin != "hello@1.0.0,other@2.0.0" {
		t.Fatalf("pin %q", c.AddonsPin)
	}
}
