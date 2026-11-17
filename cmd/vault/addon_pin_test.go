package main

import (
	"testing"

	"github.com/antresol-app/antresol/internal/config"
)

// TestAddonPins: VAULT_ADDONS_PIN is parsed at start; a malformed one, or
// a pin with addons off, stops the server.
func TestAddonPins(t *testing.T) {
	if p, err := addonPins(config.Config{Addons: true}); err != nil || p != nil {
		t.Fatalf("none: %v, %v", p, err)
	}
	if p, err := addonPins(config.Config{AddonsPin: ""}); err != nil || p != nil {
		t.Fatalf("none, addons off: %v, %v", p, err)
	}
	p, err := addonPins(config.Config{Addons: true, AddonsPin: "hello@1.0.0"})
	if err != nil || len(p) != 1 || p[0].ID != "hello" || p[0].Version != "1.0.0" {
		t.Fatalf("one: %v, %v", p, err)
	}
	if _, err := addonPins(config.Config{Addons: true, AddonsPin: "hello"}); err == nil {
		t.Fatal("a malformed pin passed")
	}
	if _, err := addonPins(config.Config{Addons: false, AddonsPin: "hello@1.0.0"}); err == nil {
		t.Fatal("a pin with VAULT_ADDONS=off passed")
	}
}
