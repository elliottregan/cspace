package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/elliottregan/cspace/internal/devcontainer"
)

// ExtractAll runs each customizations.cspace.extractCredentials entry
// against its target sidecar (which must already be healthy), captures
// stdout, and stores the result on Orchestration.ExtractedEnv. cmd_up
// reads ExtractedEnv after Up completes and injects each entry into
// the sandbox environment.
//
// Trim defaults to true: trailing whitespace is stripped from captured
// stdout (Convex's generate_admin_key.sh appends a newline that would
// break env parsing).
func (o *Orchestration) ExtractAll(ctx context.Context) error {
	if o.ExtractedEnv == nil {
		o.ExtractedEnv = map[string]string{}
	}
	for _, ec := range o.Plan.Devcontainer.Customizations.Cspace.ExtractCredentials {
		val, err := extractOne(ctx, o.Substrate, o.containerName(ec.From), ec, o.Plan.Devcontainer.ShouldTrim(ec))
		if err != nil {
			return fmt.Errorf("extractCredentials %q: %w", ec.Env, err)
		}
		o.ExtractedEnv[ec.Env] = val
	}
	return nil
}

func extractOne(ctx context.Context, sub Substrate, container string, ec devcontainer.ExtractCredential, trim bool) (string, error) {
	stdout, err := sub.Exec(ctx, container, ec.Exec)
	if err != nil {
		return "", err
	}
	if trim {
		stdout = strings.TrimRight(stdout, "\r\n\t ")
	}
	return stdout, nil
}
