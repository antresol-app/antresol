package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/antresol-app/antresol/internal/config"
	"github.com/antresol-app/antresol/internal/discovery"
	"github.com/antresol-app/antresol/internal/version"
)

// runDiscovery lets the apps find this server on the home network (mDNS
// and the UDP probe responder, see internal/discovery) until ctx ends.
func runDiscovery(ctx context.Context, cfg config.Config, serverID string, local *discovery.Status) {
	if !cfg.MDNS && cfg.DiscoveryPort == 0 {
		return
	}
	port := 8420
	if _, p, err := net.SplitHostPort(cfg.Listen); err == nil {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
	}
	info := discovery.Info{ID: serverID, Name: cfg.ServerName, Version: version.Version, Port: port, Scheme: "http"}
	if cfg.PublicURL != "" {
		info.Extra = []string{cfg.PublicURL}
	}
	discovery.Run(ctx, discovery.Config{Info: info, MDNS: cfg.MDNS, UDPPort: cfg.DiscoveryPort, Log: slog.Default(), Status: local,
		ShareDir: cfg.AnnounceDir})
}

// announceCommand is the mDNS announcer: `vault mdns-announce`, the
// antresol-mdns service of compose.yml (internal/discovery/share.go). It
// reads no config and no database: only VAULT_ANNOUNCE_DIR (default
// /announce, the folder shared with the server) and VAULT_ANNOUNCE_PORT
// (the HTTP port published for the server on the host; empty: the
// server's own).
const announceCommand = "mdns-announce"

func runAnnounce(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%s takes no arguments (VAULT_ANNOUNCE_DIR, VAULT_ANNOUNCE_PORT)", announceCommand)
	}
	dir := strings.TrimSpace(os.Getenv("VAULT_ANNOUNCE_DIR"))
	if dir == "" {
		dir = "/announce"
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("VAULT_ANNOUNCE_DIR: %s is not a folder shared with the server", dir)
	}
	port := 0
	if v := strings.TrimSpace(os.Getenv("VAULT_ANNOUNCE_PORT")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return fmt.Errorf("VAULT_ANNOUNCE_PORT: %q is not a port", v)
		}
		port = n
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	discovery.Announce(ctx, discovery.AnnounceConfig{Dir: dir, Port: port, Log: slog.Default()})
	return nil
}
