package devcontainer

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/tailscale/hujson"
)

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read devcontainer.json: %w", err)
	}
	standard, err := hujson.Standardize(raw)
	if err != nil {
		return nil, fmt.Errorf("strip JSONC: %w", err)
	}

	var unknown map[string]any
	if err := json.Unmarshal(standard, &unknown); err != nil {
		return nil, fmt.Errorf("parse devcontainer.json: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(standard, &cfg); err != nil {
		return nil, fmt.Errorf("decode devcontainer.json: %w", err)
	}
	cfg.SourcePath = path
	cfg.Unknown = filterUnknown(unknown)
	return &cfg, nil
}

// supportedFields lists every devcontainer.json key cspace honors.
// Anything else lands in Config.Unknown for the validator to reject.
var supportedFields = map[string]bool{
	"name": true, "image": true, "dockerFile": true, "build": true,
	"dockerComposeFile": true, "service": true, "runServices": true,
	"workspaceFolder": true, "containerEnv": true, "mounts": true,
	"forwardPorts": true, "portsAttributes": true,
	"postCreateCommand": true, "postStartCommand": true,
	"remoteUser": true, "features": true, "customizations": true,
}

func filterUnknown(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := map[string]any{}
	for k, v := range in {
		if !supportedFields[k] {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
