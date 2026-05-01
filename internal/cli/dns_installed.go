package cli

import "os"

// dnsInstalled returns true when /etc/resolver/cspace2.local exists.
//
// Used by cspace2-up and cspace ports to decide between friendly-URL
// rendering (`http://<sandbox>.cspace2.local/`) and IP-URL rendering
// (`http://<ip>/`). The path constant `dnsResolverFile` lives in
// cmd_dns.go where it is also referenced.
func dnsInstalled() bool {
	_, err := os.Stat(dnsResolverFile)
	return err == nil
}
