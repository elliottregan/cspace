// Package secrets loads cspace-owned secrets from the project's .cspace/
// directory and the user's ~/.cspace/, merging them with project-local
// values overriding global ones.
//
// File format is dotenv-style: KEY=value, # comments, blank lines.
// Quoted values (single or double) have surrounding quotes stripped.
//
// SECURITY NOTE: secrets passed to the substrate via process env (-e flags)
// may be logged by the runtime's init process (Apple Container's vminitd
// is known to do this). Treat anything in these files as readable by anyone
// with `container logs` access on the host. P1 will add Keychain-backed
// values (KEY=keychain:<service-name>) and an alternative delivery path that
// doesn't transit -e.
package secrets

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cspaceKeys is the built-in list of secret keys whose values are looked up
// in the macOS Keychain by Load() under service name "cspace-<KEY>" and
// (where applicable) auto-discovered from host state on macOS.
// File-based values in ~/.cspace/secrets.env or <project>/.cspace/secrets.env
// override the Keychain layer per the documented precedence.
var cspaceKeys = []string{
	"ANTHROPIC_API_KEY",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"GITHUB_PERSONAL_ACCESS_TOKEN",
}

// SecretKeys returns the canonical list of cspace-owned secret env var
// names (a copy, so callers can't mutate cspaceKeys). Used by cmd_up.go to
// detect when a project's compose env_file silently overrides a
// cspace-delivered secret.
func SecretKeys() []string {
	out := make([]string, len(cspaceKeys))
	copy(out, cspaceKeys)
	return out
}

// discoverClaudeOauthToken and discoverGhAuthToken are package-level function
// variables so tests can swap them with stubs without exec'ing real binaries.
var (
	discoverClaudeOauthToken = DiscoverClaudeOauthToken
	discoverGhAuthToken      = DiscoverGhAuthToken
)

// timeNow is a package-level seam so tests can pin "now" when exercising
// OAuth-token expiry handling.
var timeNow = time.Now

// OAuthExpired reports whether a discovered Claude Code OAuth access token's
// expiry is known and already in the past. A zero expiresAt — older Claude
// Code builds that don't record one — is treated as NOT expired, to preserve
// historical behavior rather than guess. cspace refuses to inject an
// auto-discovered token for which this returns true: a short-lived access
// token (sk-ant-oat-…) that has lapsed yields a sandbox that boots but fails
// every SDK call with a confusing auth error, so it is better to surface the
// missing credential up front.
func OAuthExpired(expiresAt time.Time) bool {
	return !expiresAt.IsZero() && !expiresAt.After(timeNow())
}

// Load returns the merged cspace secrets for a project. Resolution order
// (later layers override earlier ones; first reachable value wins):
//
//  1. ~/.cspace/secrets.env       (user-global manual)
//  2. <projectRoot>/.cspace/secrets.env  (project-scoped lock-in; wins over user)
//  3. macOS Keychain cspace-<KEY>  (user explicitly stored via `cspace keychain init`)
//  4. macOS auto-discovery        (Claude Code OAuth blob; gh auth token)
//
// Note layer order: file values WIN over Keychain. This is intentional —
// project owners who want to lock a specific PAT or API key in a file
// shouldn't have it shadowed by ambient Keychain state.
//
// Shell env (os.Getenv) is NOT applied here; cmd_up.go handles shell env
// as the highest-precedence override after this function returns.
//
// Missing files / missing Keychain entries are not errors. On non-darwin
// platforms the Keychain layers are no-ops.
func Load(projectRoot string) (map[string]string, error) {
	out := map[string]string{}

	// Layers 1+2: file loaders (project overrides user). Builds the initial map.
	home, err := os.UserHomeDir()
	if err != nil {
		// No home dir — fall through to project-only.
		home = ""
	}
	fileMap, err := loadFromDirs(home, projectRoot)
	if err != nil {
		return nil, err
	}
	for k, v := range fileMap {
		out[k] = v
	}

	// Layer 3: cspace-<KEY> Keychain entries — only fill in keys NOT already set.
	for _, key := range cspaceKeys {
		if _, present := out[key]; present {
			continue
		}
		val, err := ReadKeychain("cspace-" + key)
		if err != nil {
			return nil, err
		}
		if val != "" {
			out[key] = val
		}
	}

	// Layer 4: auto-discovery — only fill in keys still missing.
	if err := autoDiscover(out); err != nil {
		return nil, err
	}

	// Layer 4b: resolve `keychain:<service>` value-prefix references in any
	// file-supplied values. Same behavior as before.
	for k, v := range out {
		if strings.HasPrefix(v, "keychain:") {
			service := strings.TrimPrefix(v, "keychain:")
			resolved, err := ReadKeychain(service)
			if err != nil {
				return nil, err
			}
			out[k] = resolved
		}
	}

	normalizeAnthropicCarrier(out)

	return out, nil
}

// normalizeAnthropicCarrier routes an Anthropic credential to the env var
// Claude Code expects based on the token's own format, regardless of which
// slot it was filed under. An OAuth token (sk-ant-oat…, e.g. sk-ant-oat01-…)
// MUST ride CLAUDE_CODE_OAUTH_TOKEN; a raw API key (sk-ant-api…) MUST ride
// ANTHROPIC_API_KEY. A token on the wrong carrier is rejected by Claude
// ("Invalid API key") and trips the interactive CLI's "custom API key" prompt.
// This makes resolution correct even when a value was misfiled (historically
// `cspace keychain init` stored everything under cspace-ANTHROPIC_API_KEY).
func normalizeAnthropicCarrier(out map[string]string) {
	if ak := out["ANTHROPIC_API_KEY"]; strings.HasPrefix(ak, "sk-ant-oat") {
		if out["CLAUDE_CODE_OAUTH_TOKEN"] == "" {
			out["CLAUDE_CODE_OAUTH_TOKEN"] = ak
		}
		delete(out, "ANTHROPIC_API_KEY")
	}
	if ot := out["CLAUDE_CODE_OAUTH_TOKEN"]; strings.HasPrefix(ot, "sk-ant-api") {
		if out["ANTHROPIC_API_KEY"] == "" {
			out["ANTHROPIC_API_KEY"] = ot
		}
		delete(out, "CLAUDE_CODE_OAUTH_TOKEN")
	}
}

// autoDiscover fills in Anthropic + GitHub credentials from host state when
// the corresponding key is not already set in `out`. This is the
// last-resort convenience layer: file values, Keychain `cspace-<KEY>`
// entries, and shell env (applied later) all take precedence.
func autoDiscover(out map[string]string) error {
	// Anthropic credential: Claude Code-credentials JSON envelope on macOS.
	if _, present := out["ANTHROPIC_API_KEY"]; !present {
		if _, present := out["CLAUDE_CODE_OAUTH_TOKEN"]; !present {
			tok, expiresAt, err := discoverClaudeOauthToken()
			if err != nil {
				return err
			}
			// Refuse to inject an already-expired auto-discovered token.
			// The host's Claude Code-credentials access token is short-lived
			// (~hours) and only refreshes when `claude` runs on the host;
			// injecting a lapsed one yields a sandbox that boots but fails
			// every SDK call with a confusing auth error. Leaving the key
			// unset instead lets cmd_up surface a clear "how to fix" hint.
			if tok != "" && !OAuthExpired(expiresAt) {
				// Single source, single fill — the cmd_up alias logic
				// takes care of mapping CLAUDE_CODE_OAUTH_TOKEN onto
				// ANTHROPIC_API_KEY. Fill CLAUDE_CODE_OAUTH_TOKEN here and
				// let the existing alias propagate (so users who ALREADY
				// have a real ANTHROPIC_API_KEY in their file aren't
				// overridden).
				out["CLAUDE_CODE_OAUTH_TOKEN"] = tok
			}
		}
	}

	// GitHub credential: `gh auth token` on any platform with gh.
	needsGh := true
	for _, key := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GITHUB_PERSONAL_ACCESS_TOKEN"} {
		if _, present := out[key]; present {
			needsGh = false
			break
		}
	}
	if needsGh {
		tok, err := discoverGhAuthToken()
		if err != nil {
			return err
		}
		if tok != "" {
			// Fill GH_TOKEN as the canonical name; cmd_up's alias
			// propagation (Task B, separate task) makes GITHUB_TOKEN /
			// GITHUB_PERSONAL_ACCESS_TOKEN see the same value.
			out["GH_TOKEN"] = tok
		}
	}

	return nil
}

// loadFromDirs is the testable core of Load. globalDir and projectDir are
// the directories that contain a `.cspace/secrets.env` (i.e. one level
// above the file).
func loadFromDirs(globalDir, projectDir string) (map[string]string, error) {
	out := map[string]string{}

	if globalDir != "" {
		if err := mergeFile(out, filepath.Join(globalDir, ".cspace", "secrets.env")); err != nil {
			return nil, err
		}
	}
	if projectDir != "" && projectDir != globalDir {
		if err := mergeFile(out, filepath.Join(projectDir, ".cspace", "secrets.env")); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func mergeFile(into map[string]string, path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = f.Close() }()

	parsed, err := parse(f)
	if err != nil {
		return err
	}
	for k, v := range parsed {
		into[k] = v
	}
	return nil
}

func parse(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue // malformed — skip
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 {
			first, last := val[0], val[len(val)-1]
			if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		out[key] = val
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
