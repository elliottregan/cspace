package credentials

import (
	"fmt"
	"strings"
	"time"
)

// SummaryLine renders the always-on credential line for `cspace up`, plus a
// warning that is non-empty only when durability or validity is actually at
// risk.
//
// It is information rather than an alarm: printed on every boot so the
// resolution model stays visible, escalating only when something needs
// doing. The line names the winning scope (keychain vs keychain:<project>),
// which is what makes a silently-detached project-scoped entry noticeable —
// renaming a directory drops resolution to the broader global credential.
func SummaryLine(b BakeResult, now time.Time, runway time.Duration) (line, warning string) {
	var segments []string
	var warnings []string

	for _, g := range []struct {
		label string
		keys  []string
	}{
		{"claude", anthropicGroup},
		{"github", githubGroup},
	} {
		cred, key, ok := firstWinner(b, g.keys)
		if !ok {
			continue
		}
		state, warn := groupState(b, cred, key, now, runway)
		segments = append(segments, fmt.Sprintf("%s ← %s (%s)", g.label, scopeLabel(cred), state))
		if warn != "" {
			warnings = append(warnings, warn)
		}
	}

	if len(segments) == 0 {
		line = "credentials: none resolved"
	} else {
		line = "credentials: " + strings.Join(segments, " · ")
	}

	if len(b.Shadowed) > 0 {
		// Compact on purpose — doctor enumerates. A project whose .env
		// legitimately carries app tokens should not get a per-key list on
		// every boot.
		line += " · project env ignored for cspace keys"
	}

	if len(b.Winners) == 0 {
		// This is also the successor to the old one-time onboarding nudge,
		// and it fires every boot rather than once: no credential is an
		// active misconfiguration, not a first-run hint.
		msg := "no cspace credential resolved. Store one with `cspace keychain init`."
		if len(b.Shadowed) > 0 {
			msg = "no cspace credential resolved, and this project's env file is " +
				"ignored for cspace-owned keys. Store one with `cspace keychain init`."
		}
		warnings = append(warnings, msg)
	}

	return line, strings.Join(warnings, " ")
}

// firstWinner returns the credential that shipped for a group, along with
// the key it shipped under.
func firstWinner(b BakeResult, keys []string) (Credential, string, bool) {
	// Presence is the signal: Bake populates Winners only for credentials
	// that actually shipped.
	for _, k := range keys {
		if c, ok := b.Winners[k]; ok {
			return c, k, true
		}
	}
	return Credential{}, "", false
}

// groupState renders the parenthesised state for one segment: a verified
// provider answer where we have one, otherwise the derived durability.
func groupState(b BakeResult, cred Credential, key string, now time.Time, runway time.Duration) (state, warning string) {
	switch b.Validities[key] {
	case ValidityRejected:
		return "rejected", fmt.Sprintf("the %s credential was rejected by the provider (401); "+
			"git and gh in the sandbox will fail until it is replaced.", key)
	case ValidityValid:
		return "verified", ""
	}

	d := DurabilityOf(cred, now, runway)
	if runway <= 0 {
		// Escalation disabled: still report accurately, never warn. An
		// expired credential is reported as expired, not merely expiring.
		return d.String(), ""
	}
	switch d {
	case Expired:
		return "expired", "the Anthropic credential has already expired. " +
			"`cspace keychain init` stores a durable one."
	case Expiring:
		return fmt.Sprintf("expires in %s", remaining(cred.ExpiresAt, now)),
			"the Anthropic credential expires during a typical session. " +
				"`cspace keychain init` stores a durable one."
	default:
		return d.String(), ""
	}
}

// scopeLabel renders the winning source, naming the project for a
// project-scoped Keychain entry so the narrower scope is visible.
func scopeLabel(c Credential) string {
	if c.Source != SourceProjectKeychain {
		return c.Source.String()
	}
	if project, ok := projectFromKeychainService(c.Detail, c.Key); ok {
		return "keychain:" + project
	}
	return "keychain:project"
}

// projectFromKeychainService pulls "<project>" out of "cspace-<project>-<KEY>".
func projectFromKeychainService(service, key string) (string, bool) {
	const prefix = "cspace-"
	suffix := "-" + key
	if !strings.HasPrefix(service, prefix) || !strings.HasSuffix(service, suffix) {
		return "", false
	}
	project := service[len(prefix) : len(service)-len(suffix)]
	if project == "" {
		return "", false
	}
	return project, true
}

func remaining(expiresAt, now time.Time) string {
	d := expiresAt.Sub(now)
	if d < time.Minute {
		return "under a minute"
	}
	d = d.Round(time.Minute)
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh%02dm", h, m)
}
