package main

import (
	"crypto/rand"
	"encoding/hex"
	"net"
)

// The loopback relay of addon inputs (/api/addon-input/…): the URL ffmpeg
// and ffprobe read an input through, and the per-process token they show.
// The core's own, apart from the in-core engine's.

// inputLoopbackBase is the URL ffmpeg uses to reach this server, "" when
// the listen address is not reachable on loopback.
func inputLoopbackBase(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		return ""
	}
	switch ip := net.ParseIP(host); {
	case host == "", host == "localhost", ip != nil && (ip.IsUnspecified() || ip.IsLoopback()):
		if ip != nil && ip.IsLoopback() && ip.To4() == nil {
			return "http://[::1]:" + port
		}
		return "http://127.0.0.1:" + port
	}
	return ""
}

// newInputToken is a random token of 128 bits, as 32 hex characters.
func newInputToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
