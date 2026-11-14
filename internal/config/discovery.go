package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// defaultDiscoveryPort is the UDP port of the discovery responder (the
// number of the HTTP port, on UDP).
const defaultDiscoveryPort = 8420

// loadDiscovery reads VAULT_DISCOVERY, VAULT_MDNS, VAULT_DISCOVERY_PORT and
// VAULT_SERVER_NAME (see Config.MDNS).
func (c *Config) loadDiscovery() error {
	c.MDNS, c.DiscoveryPort = true, defaultDiscoveryPort
	c.ServerName = strings.TrimSpace(os.Getenv("VAULT_SERVER_NAME"))
	if c.ServerName == "" {
		c.ServerName = "Antresol"
	}
	on, err := onOff("VAULT_DISCOVERY", true)
	if err != nil {
		return err
	}
	if !on {
		c.MDNS, c.DiscoveryPort = false, 0
		return nil
	}
	if c.MDNS, err = onOff("VAULT_MDNS", true); err != nil {
		return err
	}
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv("VAULT_DISCOVERY_PORT"))); v {
	case "":
	case "off", "0":
		c.DiscoveryPort = 0
	default:
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("VAULT_DISCOVERY_PORT: %q is not a port or off", v)
		}
		c.DiscoveryPort = n
	}
	return nil
}

// onOff reads an on/off variable; def when it is unset.
func onOff(key string, def bool) (bool, error) {
	switch v := strings.ToLower(strings.TrimSpace(os.Getenv(key))); v {
	case "":
		return def, nil
	case "on", "1", "true", "yes":
		return true, nil
	case "off", "0", "false", "no":
		return false, nil
	default:
		return false, fmt.Errorf("%s: %q is not on or off", key, v)
	}
}
