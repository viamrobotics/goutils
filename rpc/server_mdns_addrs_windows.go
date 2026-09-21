package rpc

import "net"

// mdnsAdvertisedIPs returns the addresses of every multicast interface, matching what was
// advertised before the address list was made per-interface.
//
// Windows cannot resolve the receiving interface: golang.org/x/net/ipv4 compiles without
// control-message support there, so ReadFrom hands zeroconf a nil ControlMessage and it sees
// interface index 0. Left with an empty address list it would answer with no A records at all
// and `<name>.local` would stop resolving, so supply the addresses up front instead.
func mdnsAdvertisedIPs(ifaces []net.Interface) []string {
	addrV4 := make([]string, 0)
	addrV6 := make([]string, 0)
	for _, iface := range ifaces {
		v4, v6 := addrsForInterface(&iface)
		addrV4 = append(addrV4, v4...)
		addrV6 = append(addrV6, v6...)
	}
	return append(addrV4, addrV6...)
}

func addrsForInterface(iface *net.Interface) ([]string, []string) {
	var v4, v6, v6local []string
	addrs, err := iface.Addrs()
	if err != nil {
		return v4, v6
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				v4 = append(v4, ipnet.IP.String())
			} else {
				switch ip := ipnet.IP.To16(); ip != nil {
				case ip.IsGlobalUnicast():
					v6 = append(v6, ipnet.IP.String())
				case ip.IsLinkLocalUnicast():
					v6local = append(v6local, ipnet.IP.String())
				}
			}
		}
	}
	if len(v6) == 0 {
		v6 = v6local
	}
	return v4, v6
}
