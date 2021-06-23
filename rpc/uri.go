package rpc

import "fmt"

// HostURI returns a RPC specific URI for a host located at an address.
func HostURI(address string, host string) string {
	return fmt.Sprintf("rpc://%s?host=%s", address, host)
}
