package main

import (
	"context"
	"log/slog"
	"net"
	"strconv"

	"github.com/antresol-app/antresol/internal/config"
	"github.com/antresol-app/antresol/internal/discovery"
	"github.com/antresol-app/antresol/internal/version"
)

// runDiscovery lets the apps find this server on the home network (mDNS
// and the UDP probe responder, see internal/discovery) until ctx ends.
func runDiscovery(ctx context.Context, cfg config.Config, serverID string) {
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
	discovery.Run(ctx, discovery.Config{Info: info, MDNS: cfg.MDNS, UDPPort: cfg.DiscoveryPort, Log: slog.Default()})
}
