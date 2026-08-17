package credentials

import "sort"

// BakeResult is the outcome of applying credential policy to a container's
// environment.
type BakeResult struct {
	Env        map[string]string     // the final container env
	Winners    map[string]Credential // which credential shipped, per key
	Shadowed   []string              // cspace-owned keys a project file set and lost
	Validities map[string]Validity   // Verify outcome per shipped key
}

// Bake produces the final container environment.
//
// appEnv arrives with the project's compose env_file and devcontainer
// containerEnv already merged. The five cspace-owned keys are stripped from
// it — unconditionally, including when cspace resolves nothing for a key —
// and replaced with cspace's own resolution. That unconditional strip is
// what makes "a project env_file cannot shadow a cspace credential" a
// guarantee rather than a best effort, and it is why a project whose .env
// was its only credential source now boots without one.
//
// Verification runs on the value that actually ships, not on the value
// resolution first picked. The old ReconcileGitHubToken checked a value the
// later env_file merge discarded, so a dead PAT reached containers
// unvalidated while a green preflight said otherwise.
func (h Host) Bake(res map[string]Resolution, appEnv map[string]string) BakeResult {
	out := BakeResult{
		Env:        make(map[string]string, len(appEnv)+len(Keys())),
		Winners:    make(map[string]Credential),
		Validities: make(map[string]Validity),
	}
	for k, v := range appEnv {
		if IsOwnedKey(k) {
			out.Shadowed = append(out.Shadowed, k)
			continue
		}
		out.Env[k] = v
	}
	sort.Strings(out.Shadowed)

	picked := make(map[string]Credential)
	validity := make(map[string]Validity)
	for _, key := range Keys() {
		c, v, ok := h.climb(res[key])
		if !ok {
			continue
		}
		picked[key] = c
		validity[key] = v
	}

	for k, c := range ApplyGroupPolicy(picked) {
		out.Env[k] = c.Value
		out.Winners[k] = c
		// Group policy may relabel a credential onto a different key
		// (Anthropic routing, GitHub mirroring), so carry the validity from
		// the slot it was verified under rather than the slot it lands in.
		if v, ok := validity[c.Key]; ok {
			out.Validities[k] = v
		} else if v, ok := validity[k]; ok {
			out.Validities[k] = v
		}
	}
	return out
}

// climb walks the candidate stack, stopping at the first credential the
// provider does not definitively reject. ValidityUnknown stops the climb:
// an offline check is not evidence of a bad token, and advancing on it
// would let a network blip silently downgrade a good credential.
func (h Host) climb(r Resolution) (Credential, Validity, bool) {
	for _, c := range r.Candidates {
		v := h.Verify(c)
		if v != ValidityRejected {
			return c, v, true
		}
	}
	// Every rung rejected: ship the top one anyway and let reporting say so.
	// Refusing to ship would turn a degraded sandbox into no sandbox.
	if c, ok := r.Winner(); ok {
		return c, ValidityRejected, true
	}
	return Credential{}, ValidityUnknown, false
}
