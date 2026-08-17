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
	return h.Select(res).Apply(appEnv)
}

// Selection is the outcome of choosing and verifying credentials, before
// any project environment is involved.
//
// Select and Apply are separate because `cspace up` needs the selection
// *before* the overlay starts — so the summary line reaches the real
// terminal rather than being shredded mid-render — but cannot apply it
// until the compose and devcontainer merges have produced the app env,
// which happens well after. Splitting them also means verification runs
// once, not once per phase.
type Selection struct {
	Winners    map[string]Credential
	Validities map[string]Validity
}

// Select walks each key's candidate stack, verifying as it goes, and
// applies group policy. It touches no project environment.
func (h Host) Select(res map[string]Resolution) Selection {
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

	// Group policy rewrites Credential.Key to the landing slot — Anthropic
	// routing moves a carrier, GitHub mirroring writes one credential under
	// three names — so the post-policy key says nothing about which
	// candidate was verified. Index validity by the credential's value,
	// which survives relabeling, or a mirrored winner inherits whatever
	// verdict its landing slot's own candidate happened to get.
	validityByValue := make(map[string]Validity, len(picked))
	for key, c := range picked {
		if v, ok := validity[key]; ok {
			validityByValue[c.Value] = v
		}
	}

	out := Selection{
		Winners:    make(map[string]Credential),
		Validities: make(map[string]Validity),
	}
	for k, c := range ApplyGroupPolicy(picked) {
		out.Winners[k] = c
		if v, ok := validityByValue[c.Value]; ok {
			out.Validities[k] = v
		}
	}
	return out
}

// Apply writes the selection over a project's assembled environment.
func (s Selection) Apply(appEnv map[string]string) BakeResult {
	out := BakeResult{
		Env:        make(map[string]string, len(appEnv)+len(Keys())),
		Winners:    s.Winners,
		Validities: s.Validities,
	}
	for k, v := range appEnv {
		if IsOwnedKey(k) {
			out.Shadowed = append(out.Shadowed, k)
			continue
		}
		out.Env[k] = v
	}
	sort.Strings(out.Shadowed)

	for k, c := range s.Winners {
		out.Env[k] = c.Value
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
