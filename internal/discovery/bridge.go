package discovery

import (
	"net/netip"
	"os"
	"strings"
)

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

// vmNetwork: the network this process sees as the host's is that of a
// virtual machine behind NAT, not the home network: Docker Desktop (on
// macOS, Windows, and Linux too) runs containers in a LinuxKit VM, WSL 2
// sits behind the NAT of Windows, Podman machine behind gvproxy. Even
// network_mode: host is the VM's network there; multicast from it never
// reaches the home network, so a claimed antresol.local would be a name
// that works for nobody.
func vmNetwork(addrs []netip.Prefix) bool {
	rel, _ := os.ReadFile("/proc/sys/kernel/osrelease")
	_, err := os.Stat("/run/.containerenv")
	return vmHost(string(rel), err == nil, addrs)
}

// gvproxyNet is the subnet Podman machine's gvproxy gives the VM.
var gvproxyNet = netip.MustParsePrefix("192.168.127.0/24")

// vmHost is vmNetwork on what it read: the kernel release (LinuxKit's and
// WSL's say so), whether this is a Podman container, and the addresses.
func vmHost(release string, podman bool, addrs []netip.Prefix) bool {
	r := strings.ToLower(release)
	if strings.Contains(r, "linuxkit") || strings.Contains(r, "microsoft") {
		return true
	}
	if !podman || len(addrs) == 0 {
		return false
	}
	for _, p := range addrs {
		if !gvproxyNet.Contains(p.Addr()) {
			return false
		}
	}
	return true
}
