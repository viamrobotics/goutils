//go:build !windows

package rpc

import "net"

// mdnsAdvertisedIPs returns the static address list for non-loopback mDNS registrations. It is
// empty everywhere but Windows, so zeroconf answers with the addresses of whichever interface
// the query arrived on.
func mdnsAdvertisedIPs([]net.Interface) []string {
	return nil
}
