package cli

import (
	"context"
	"io"

	"github.com/elliottregan/cspace/internal/control"
	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// cliHost implements control.Host over the two host operations whose
// implementations still live in this package: teardownSandbox (cmd_down.go)
// and the browser restart ladder (browser.go). It exists so internal/control
// can run them without importing internal/cli, keeping that dependency
// one-way.
type cliHost struct {
	adapter  *applecontainer.Adapter
	registry *registry.Registry
}

var _ control.Host = (*cliHost)(nil)

func newCLIHost(a *applecontainer.Adapter, r *registry.Registry) *cliHost {
	return &cliHost{adapter: a, registry: r}
}

func (h *cliHost) Teardown(ctx context.Context, project, sandbox string, out io.Writer, wipeState bool) {
	teardownSandbox(ctx, h.adapter, h.registry, project, sandbox, out, wipeState)
}

// RestartBrowser goes through restartBrowserFn, the same var-seam the
// daemon's POST /browser/restart/{project} handler uses, so a test can fake
// the ladder's outcome without touching real containers. An empty version
// lets the ladder pin the running sidecar's version or fall back to the
// default.
func (h *cliHost) RestartBrowser(ctx context.Context, project string) error {
	_, err := restartBrowserFn(ctx, project, "")
	return err
}
