package cli

import (
	"context"
	"time"

	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// legacyVmnetGateway is Apple Container's pre-1.0 default vmnet gateway. Used
// as a last-resort fallback when the live gateway can't be derived (e.g. the
// apiserver is momentarily unreachable), so a derivation failure degrades to
// the old behavior rather than breaking outright.
const legacyVmnetGateway = "192.168.64.1"

// resolveHostGateway returns the vmnet gateway IP the daemon's DNS and registry
// HTTP are reachable at from inside sandboxes (e.g. "192.168.65.1" on Container
// 1.1.x). It discovers the live value via `container network inspect default`
// rather than hardcoding it — Apple moved the subnet 192.168.64 -> 192.168.65
// at the 1.0 boundary. Falls back to legacyVmnetGateway on any error.
func resolveHostGateway(ctx context.Context) string {
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if gw, err := applecontainer.New().NetworkGateway(dctx, "default"); err == nil && gw != "" {
		return gw
	}
	return legacyVmnetGateway
}
