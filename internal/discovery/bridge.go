package discovery

import "os"

// link is a network interface as bridgeNetwork judges it.
type link struct {
	name     string
	kind     string // the kernel's link kind: veth, bridge, macvlan, tun, … ("" for a real device)
	up       bool
	loopback bool
}

// containerMarkers are the files Docker and Podman put in a container's root.
var containerMarkers = []string{"/.dockerenv", "/run/.containerenv"}

func inContainer() bool {
	for _, f := range containerMarkers {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}

// bridgeNetwork: this process runs in a container whose network is a
// NAT'd one of its own (Docker's default bridge, a compose network,
// Podman's), where mDNS multicast never reaches the home network.
func bridgeNetwork() bool {
	if !inContainer() {
		return false
	}
	links, err := netLinks()
	if err != nil {
		return false // not known: behave as before
	}
	return natOnly(links)
}

// natOnly: every interface that is up is the container's end of a veth
// pair (or the tap of rootless Podman), and the host's own Docker bridge
// is not in sight. With network_mode: host the container sees the host's
// interfaces: a real device or a bridge, and docker0 itself (also in a
// system-container guest, whose own eth0 is a veth). A macvlan or ipvlan network puts
// the container on the home network for real: not NAT.
func natOnly(links []link) bool {
	seen := false
	for _, l := range links {
		if l.name == "docker0" {
			return false
		}
		if !l.up || l.loopback {
			continue
		}
		if l.kind != "veth" && l.kind != "tun" {
			return false
		}
		seen = true
	}
	return seen
}
