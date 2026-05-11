package cli

import "os"

// dnsInstalled returns true when /etc/resolver/cspace.test exists.
//
// Used by cspace up and cspace ports to decide between friendly-URL
// rendering (`http://<sandbox>.cspace.test/`) and IP-URL rendering
// (`http://<ip>/`). The path constant `dnsResolverFile` lives in
// cmd_dns.go where it is also referenced.
func dnsInstalled() bool {
	_, err := os.Stat(dnsResolverFile)
	return err == nil
}
