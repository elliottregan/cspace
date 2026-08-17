package credentials

import (
	"fmt"
	"time"
)

// Host holds the external dependencies resolution needs, as swappable
// function fields. This is the same package-seam pattern internal/secrets
// already uses, and it is why no test in this package touches the real
// Keychain, real gh, or the network.
type Host struct {
	// ReadKeychain reads a generic password by service name. A missing item
	// must return ("", nil); a non-nil error means the read genuinely failed
	// (e.g. the keychain is locked) and is propagated.
	ReadKeychain func(service string) (string, error)
	// LookupEnv reads ambient host-shell environment.
	LookupEnv func(key string) (string, bool)
	// DiscoverClaude returns the host's Claude Code OAuth access token and
	// its expiry, or ("", zero, nil) when there is none.
	DiscoverClaude func() (string, time.Time, error)
	// DiscoverGh returns `gh auth token`, or ("", nil) when there is none.
	DiscoverGh func() (string, error)
	// VerifyGitHub reports whether GitHub accepts a token. Nil means
	// verification is unavailable, which yields ValidityUnknown.
	VerifyGitHub func(token string) Validity
	// Now is a seam for expiry decisions. Nil means time.Now.
	Now func() time.Time
}

func (h Host) now() time.Time {
	if h.Now == nil {
		return time.Now()
	}
	return h.Now()
}

// expired reports whether a known expiry is already in the past. A zero
// expiry — older Claude Code builds that record none — is treated as not
// expired rather than guessed at.
func expired(expiresAt, now time.Time) bool {
	return !expiresAt.IsZero() && !expiresAt.After(now)
}

// Resolve returns the ranked candidate stack for every cspace-owned key,
// swallowing a keychain read failure. Use ResolveErr where a locked
// keychain should be surfaced rather than silently treated as empty.
func (h Host) Resolve(project string, envFlags map[string]string) map[string]Resolution {
	out, _ := h.ResolveErr(project, envFlags)
	return out
}

// ResolveErr collects every candidate cspace could deliver, ranked. It
// collects; it does not choose. Winner() chooses, and Bake's 401 ladder
// re-chooses — which is why the losing candidates must survive.
//
// A project's compose env_file and devcontainer containerEnv are absent by
// construction: they are not sources here, so they cannot shadow anything.
// That is what makes the guarantee absolute rather than best-effort.
func (h Host) ResolveErr(project string, envFlags map[string]string) (map[string]Resolution, error) {
	out := make(map[string]Resolution, len(Keys()))

	claudeTok, claudeExp := h.claude()
	ghTok := h.gh()

	var firstErr error
	keychain := func(service string) string {
		v, err := h.readKeychain(service)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("read keychain %s: %w", service, err)
		}
		return v
	}

	for _, key := range Keys() {
		r := Resolution{Key: key}

		if v := envFlags[key]; v != "" {
			r.add(key, v, SourceEnvFlag, "--env "+key, time.Time{})
		}
		if project != "" {
			svc := "cspace-" + project + "-" + key
			if v := keychain(svc); v != "" {
				r.add(key, v, SourceProjectKeychain, svc, time.Time{})
			}
		}
		globalSvc := "cspace-" + key
		if v := keychain(globalSvc); v != "" {
			r.add(key, v, SourceGlobalKeychain, globalSvc, time.Time{})
		}
		if h.LookupEnv != nil {
			if v, ok := h.LookupEnv(key); ok && v != "" {
				r.add(key, v, SourceHostShell, "$"+key, time.Time{})
			}
		}
		switch key {
		case KeyClaudeOAuthToken:
			// Auto-discovery fills only the OAuth carrier: the host's
			// Claude Code-credentials blob is always an sk-ant-oat access
			// token. ANTHROPIC_API_KEY has no auto-discovered source.
			//
			// An already-expired discovered token is refused outright rather
			// than offered as a candidate. Injecting one yields a sandbox
			// that boots and then fails every SDK call with a confusing auth
			// error; leaving the key unset lets reporting say what to do.
			// Only auto-discovery is filtered this way — an explicitly
			// stored credential is the user's call, not ours.
			if claudeTok != "" && !expired(claudeExp, h.now()) {
				r.add(key, claudeTok, SourceAutoDiscovered, "Claude Code-credentials", claudeExp)
			}
		case KeyGHToken, KeyGitHubToken, KeyGitHubPAT:
			if ghTok != "" {
				r.add(key, ghTok, SourceAutoDiscovered, "gh auth token", time.Time{})
			}
		}

		out[key] = r
	}
	return out, firstErr
}

func (r *Resolution) add(key, value string, src Source, detail string, exp time.Time) {
	r.Candidates = append(r.Candidates, Credential{
		Key:       key,
		Value:     value,
		Source:    src,
		Detail:    detail,
		ExpiresAt: exp,
	})
}

func (h Host) readKeychain(service string) (string, error) {
	if h.ReadKeychain == nil {
		return "", nil
	}
	return h.ReadKeychain(service)
}

func (h Host) claude() (string, time.Time) {
	if h.DiscoverClaude == nil {
		return "", time.Time{}
	}
	tok, exp, err := h.DiscoverClaude()
	if err != nil {
		return "", time.Time{}
	}
	return tok, exp
}

func (h Host) gh() string {
	if h.DiscoverGh == nil {
		return ""
	}
	tok, err := h.DiscoverGh()
	if err != nil {
		return ""
	}
	return tok
}
