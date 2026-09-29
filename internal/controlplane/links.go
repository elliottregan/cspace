package controlplane

import "context"

// LinkOpener opens a web link in the host's browser. Implementations must reject
// non-web URLs. The UI calls it from a bounded command, never from Update/View.
type LinkOpener interface {
	OpenURL(context.Context, string) error
}
