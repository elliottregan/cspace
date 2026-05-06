// Package features resolves devcontainer.json `features` entries against
// cspace's built-in feature list. Other feature IDs hard-reject in v1.0;
// registry-driven support (downloading arbitrary feature tarballs) is
// planned for v1.1.
package features

import (
	"fmt"
	"sort"
)

// supportedIDs maps the feature ID (per devcontainer spec) to the script
// filename under /opt/cspace/features/ inside the sandbox.
var supportedIDs = map[string]string{
	"ghcr.io/devcontainers/features/node:1":             "node.sh",
	"ghcr.io/devcontainers/features/python:1":           "python.sh",
	"ghcr.io/devcontainers/features/common-utils:1":     "common-utils.sh",
	"ghcr.io/devcontainers/features/docker-in-docker:1": "docker-in-docker.sh",
	"ghcr.io/devcontainers/features/git:1":              "git.sh",
	"ghcr.io/devcontainers/features/github-cli:1":       "github-cli.sh",
}

// Resolved is one feature ready for the entrypoint to execute.
type Resolved struct {
	ID     string
	Script string         // absolute path inside the microVM
	Args   map[string]any // surface as FEATURE_<UPPER>=<val> env vars
}

// Resolve translates a devcontainer.json features map into the in-microVM
// script paths. Returns a named error for any unsupported feature ID.
func Resolve(features map[string]any) ([]Resolved, error) {
	if len(features) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(features))
	for id := range features {
		ids = append(ids, id)
	}
	sort.Strings(ids) // deterministic order
	var out []Resolved
	for _, id := range ids {
		script, ok := supportedIDs[id]
		if !ok {
			return nil, fmt.Errorf("feature %q not supported in v1.0; supported: %s", id, supportedKeysJoined())
		}
		argMap, _ := features[id].(map[string]any)
		out = append(out, Resolved{
			ID:     id,
			Script: "/opt/cspace/features/" + script,
			Args:   argMap,
		})
	}
	return out, nil
}

func supportedKeysJoined() string {
	keys := make([]string, 0, len(supportedIDs))
	for k := range supportedIDs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := ""
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += k
	}
	return out
}
