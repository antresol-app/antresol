package discovery

import (
	"encoding/binary"
	"syscall"
)

// netLinks lists the interfaces with their kind (IFLA_INFO_KIND), read
// from the kernel over rtnetlink.
func netLinks() ([]link, error) {
	rib, err := syscall.NetlinkRIB(syscall.RTM_GETLINK, syscall.AF_UNSPEC)
	if err != nil {
		return nil, err
	}
	msgs, err := syscall.ParseNetlinkMessage(rib)
	if err != nil {
		return nil, err
	}
	var out []link
	for i := range msgs {
		m := &msgs[i]
		if m.Header.Type != syscall.RTM_NEWLINK || len(m.Data) < syscall.SizeofIfInfomsg {
			continue
		}
		flags := binary.NativeEndian.Uint32(m.Data[8:12])
		l := link{up: flags&syscall.IFF_UP != 0, loopback: flags&syscall.IFF_LOOPBACK != 0}
		attrs, err := syscall.ParseNetlinkRouteAttr(m)
		if err != nil {
			continue
		}
		for _, a := range attrs {
			switch a.Attr.Type {
			case syscall.IFLA_IFNAME:
				l.name = cString(a.Value)
			case syscall.IFLA_LINKINFO:
				l.kind = linkKind(a.Value)
			}
		}
		out = append(out, l)
	}
	return out, nil
}

// linkKind reads IFLA_INFO_KIND (1) out of the nested IFLA_LINKINFO.
func linkKind(b []byte) string {
	for len(b) >= 4 {
		l := int(binary.NativeEndian.Uint16(b))
		if l < 4 || l > len(b) {
			return ""
		}
		if binary.NativeEndian.Uint16(b[2:]) == 1 {
			return cString(b[4:l])
		}
		b = b[min((l+3)&^3, len(b)):]
	}
	return ""
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
