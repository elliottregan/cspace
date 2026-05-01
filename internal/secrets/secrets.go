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
)

// cspaceKeys is the built-in list of secret keys whose values are looked up
// in the macOS Keychain by Load() under service name "cspace-<KEY>".
// File-based values in ~/.cspace/secrets.env or <project>/.cspace/secrets.env
// override the Keychain layer per the documented precedence.
var cspaceKeys = []string{
	"ANTHROPIC_API_KEY",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"GH_TOKEN",
	"GITHUB_TOKEN",
}

// Load returns the merged cspace secrets for a project. Resolution order
// (later layers override earlier ones):
//
//  1. macOS Keychain entries for known cspace keys (service "cspace-<KEY>").
//  2. ~/.cspace/secrets.env
//  3. <projectRoot>/.cspace/secrets.env
//  4. value-prefix references of the form `keychain:<service>` are resolved
//     against the Keychain in-place.
//
// Missing files are not errors. On non-darwin platforms the Keychain layers
// are no-ops.
func Load(projectRoot string) (map[string]string, error) {
	out := map[string]string{}

	// Layer 1: Keychain for known cspace keys.
	for _, key := range cspaceKeys {
		val, err := ReadKeychain("cspace-" + key)
		if err != nil {
			return nil, err
		}
		if val != "" {
			out[key] = val
		}
	}

	// Layers 2 + 3: file loaders. Project overrides global, both override
	// Keychain.
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

	// Layer 4: resolve `keychain:<service>` value-prefix references.
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

	return out, nil
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
	defer f.Close()

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
