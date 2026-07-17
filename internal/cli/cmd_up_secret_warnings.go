package cli

import "sort"

// envFileSecretCollisions reports which of secretKeys were delivered by
// cspace (present with a non-empty value in secretValues, a snapshot taken
// right after secrets.Load's values were merged into the env map) but ended
// up different in mergedEnv after a later merge layer — e.g. a project's
// compose env_file (.env / .env.cspace) redeclaring or blanking the key.
//
// This is a pure, read-only check: it never mutates mergedEnv and the
// caller must not change merge behavior based on its result. Per
// docs/env-cspace.md's "Precedence (stated honestly)" section, the
// env_file value is allowed to win — this only makes that footgun loud
// instead of silent.
//
// The returned slice is sorted for deterministic warning output.
func envFileSecretCollisions(secretValues, mergedEnv map[string]string, secretKeys []string) []string {
	var collisions []string
	for _, key := range secretKeys {
		delivered, ok := secretValues[key]
		if !ok || delivered == "" {
			continue // cspace didn't deliver this secret; nothing to protect
		}
		if mergedEnv[key] != delivered {
			collisions = append(collisions, key)
		}
	}
	sort.Strings(collisions)
	return collisions
}
