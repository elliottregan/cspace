package cli

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"

	"github.com/elliottregan/cspace/internal/controlplane"
)

type linkOpener struct {
	run func(context.Context, string, ...string) ([]byte, error)
}

var _ controlplane.LinkOpener = (*linkOpener)(nil)

func newLinkOpener() *linkOpener {
	return &linkOpener{run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}}
}

// OpenURL deliberately passes the URL as one argv item. Header links can come
// from project configuration; neither shell expansion nor other URL schemes
// belong in a web-link action.
func (o *linkOpener) OpenURL(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("open link: expected an HTTP or HTTPS URL")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("open link: %w", err)
	}
	out, err := o.run(ctx, "open", "--", raw)
	if err != nil {
		return execError(ctx, err, "open", "open not found: opening links needs macOS", string(out))
	}
	return nil
}
