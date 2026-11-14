//go:build !unix

package discovery

import (
	"errors"
	"net"
	"net/netip"
)

func listenMDNS([]netip.Addr) (*net.UDPConn, error) {
	return nil, errors.New("discovery: mDNS is not supported on this system")
}

func sendVia(conn *net.UDPConn, _ netip.Addr, b []byte) error {
	_, err := conn.WriteToUDPAddrPort(b, mdnsGroup)
	return err
}
