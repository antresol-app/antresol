package discovery

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/netip"
	"time"
)

// Probe is what a client sends (broadcast to 255.255.255.255 or the
// subnet's broadcast address, or unicast) to the UDP port; anything after
// it (a newline, a JSON object) is ignored.
const Probe = "antresol-discover"

// Answer is the UDP responder's JSON answer, sent back to the probe's
// source address and port.
type Answer struct {
	Service  string   `json:"service"` // always "antresol"
	Protocol int      `json:"protocol"`
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Scheme   string   `json:"scheme"`
	Port     int      `json:"port"`
	URLs     []string `json:"urls"`
}

// maxAnswer keeps an answer in one unfragmented datagram.
const maxAnswer = 1200

func (c *Config) answer(peer netip.Addr) []byte {
	a := Answer{Service: "antresol", Protocol: Protocol, ID: c.Info.ID, Name: c.Info.Name, Version: c.Info.Version,
		Scheme: c.Info.Scheme, Port: c.Info.Port, URLs: c.urls(peer)}
	for {
		b, _ := json.Marshal(a)
		if len(b) <= maxAnswer || len(a.URLs) == 0 {
			return b
		}
		a.URLs = a.URLs[:len(a.URLs)-1]
	}
}

// serveUDP answers probes on conn until ctx ends (it closes conn).
func serveUDP(ctx context.Context, conn *net.UDPConn, c *Config) {
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()
	lim := newLimiter(20) // answers per second, all clients together
	buf := make([]byte, 1500)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			c.log().Warn("discovery: UDP read", "err", err)
			return
		}
		if !bytes.HasPrefix(buf[:n], []byte(Probe)) || !lanClient(from.Addr()) || !lim.allow(time.Now()) {
			continue
		}
		if _, err := conn.WriteToUDPAddrPort(c.answer(from.Addr()), from); err != nil {
			c.log().Debug("discovery: UDP answer", "to", from, "err", err)
		}
	}
}
