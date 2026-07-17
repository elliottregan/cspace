package cli

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// containerExecFn runs argv inside the named container and returns stdout.
// Matches the shape of the substrate adapter's Exec method, adapted to a
// variadic argv for callers that don't need the full ExecOpts surface.
type containerExecFn func(ctx context.Context, container string, argv ...string) ([]byte, error)

// verifyInContainerResolution confirms that host resolves inside container
// via `getent hosts`. Cold boot is racy for a few seconds — the in-container
// dnsmasq and the cspace daemon's gateway bind both need a moment to come
// up — so this retries up to 3 times with a 2s pause between attempts
// (~4s worst case) before giving up.
func verifyInContainerResolution(ctx context.Context, exec containerExecFn, container, host string) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		out, err := exec(ctx, container, "getent", "hosts", host)
		if err != nil {
			lastErr = fmt.Errorf("resolve %s in %s: %w", host, container, err)
			continue
		}
		if strings.TrimSpace(string(out)) != "" {
			return nil
		}
		lastErr = fmt.Errorf("%s did not resolve inside %s (cspace daemon DNS may be down)", host, container)
	}
	return lastErr
}
