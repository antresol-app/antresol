//go:build unix

package discovery

import (
	"context"
	"net"
	"net/netip"
	"syscall"
)

// listenMDNS opens 0.0.0.0:5353 beside the system's own responder (Avahi,
// mDNSResponder: SO_REUSEADDR and SO_REUSEPORT) and joins the mDNS group
// on every interface in ifaces.
func listenMDNS(ifaces []netip.Addr) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			if serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); serr != nil {
				return
			}
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEPORT, 1)
		})
		if err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", ":5353")
	if err != nil {
		return nil, err
	}
	conn := pc.(*net.UDPConn)
	raw, err := conn.SyscallConn()
	if err != nil {
		conn.Close()
		return nil, err
	}
	joined := 0
	var jerr error
	raw.Control(func(fd uintptr) {
		syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, 255)
		for _, a := range ifaces {
			m := &syscall.IPMreq{Multiaddr: mdnsGroup.Addr().As4(), Interface: a.As4()}
			if err := syscall.SetsockoptIPMreq(int(fd), syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, m); err != nil {
				jerr = err
				continue
			}
			joined++
		}
	})
	if joined == 0 {
		conn.Close()
		if jerr == nil {
			jerr = errNoInterface
		}
		return nil, jerr
	}
	return conn, nil
}

// sendVia sends b to the mDNS group out of the interface with address a.
func sendVia(conn *net.UDPConn, a netip.Addr, b []byte) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	raw.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInet4Addr(int(fd), syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, a.As4())
	})
	if serr != nil {
		return serr
	}
	_, err = conn.WriteToUDPAddrPort(b, mdnsGroup)
	return err
}
