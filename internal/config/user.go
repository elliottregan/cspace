package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elliottregan/cspace/internal/assets"
)

// UserConfigPath is the user-level cspace config file: ~/.cspace/config.json.
// It sits beside the registry, the session tree and the daemon log rather
// than in a project, because what it configures — `cspace tui` — spans every
// project on the host.
func UserConfigPath(home string) string {
	return filepath.Join(home, ".cspace", "config.json")
}

// LoadUser merges the embedded defaults with the user-level config file.
//
// Unlike Load it needs no project root and no git repository: the dashboard
// it serves shows every project on the host and may be started from
// anywhere, including a directory that is no project at all. A missing file
// is not an error — the defaults are then the whole answer. A malformed one
// is, because silently ignoring it would leave a person staring at bindings
// they believe they changed.
func LoadUser(home string) (*Config, error) {
	defaultsBytes, err := assets.DefaultsJSON()
	if err != nil {
		return nil, fmt.Errorf("reading embedded defaults.json: %w", err)
	}
	var base map[string]interface{}
	if err := json.Unmarshal(defaultsBytes, &base); err != nil {
		return nil, fmt.Errorf("parsing defaults.json: %w", err)
	}

	path := UserConfigPath(home)
	if data, err := os.ReadFile(path); err == nil {
		var overlay map[string]interface{}
		if err := json.Unmarshal(data, &overlay); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}
		base = DeepMerge(base, overlay)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	merged, err := json.Marshal(base)
	if err != nil {
		return nil, fmt.Errorf("marshaling merged config: %w", err)
	}
	cfg := &Config{}
	if err := json.Unmarshal(merged, cfg); err != nil {
		return nil, fmt.Errorf("unmarshaling config into struct: %w", err)
	}
	// No autoDetect: that fills project name/repo/prefix from a project
	// root this loader deliberately does not have.
	return cfg, nil
}
