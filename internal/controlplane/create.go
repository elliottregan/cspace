package controlplane

import "context"

// SandboxNamer lets the new-container dialog use the CLI's existing naming
// rules. SuggestName may read the registry and runs outside the UI goroutine;
// ValidateName performs only a local shape check suitable for form validation.
type SandboxNamer interface {
	SuggestName(context.Context, string) (string, error)
	ValidateName(project, name string) error
}
