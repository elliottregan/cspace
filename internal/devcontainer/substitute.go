package devcontainer

import (
	"os"
	"regexp"
)

// localEnvPattern matches the devcontainer-spec variable substitution
// syntax for `${localEnv:VARIABLE_NAME}` and
// `${localEnv:VARIABLE_NAME:default}`. See
// https://containers.dev/implementors/json_reference/#variables-in-devcontainerjson.
var localEnvPattern = regexp.MustCompile(`\$\{localEnv:([A-Za-z_][A-Za-z0-9_]*)(?::([^}]*))?\}`)

// substituteLocalEnv resolves any ${localEnv:VAR} or ${localEnv:VAR:default}
// references in s against the host process's environment. Missing variables
// without a default substitute to the empty string (matching devcontainer
// spec behavior).
func substituteLocalEnv(s string) string {
	return localEnvPattern.ReplaceAllStringFunc(s, func(match string) string {
		groups := localEnvPattern.FindStringSubmatch(match)
		if len(groups) < 2 {
			return match
		}
		name := groups[1]
		def := ""
		if len(groups) >= 3 {
			def = groups[2]
		}
		if v, ok := os.LookupEnv(name); ok {
			return v
		}
		return def
	})
}

// resolveLocalEnv walks the supported substitution sites in a Config
// (containerEnv values, postCreateCommand, postStartCommand) and applies
// `${localEnv:VAR}` substitution against the host environment. The
// devcontainer spec lists more sites (mounts, runArgs, etc.); we cover
// the ones our subset honors. Idempotent — values without ${localEnv:...}
// pass through unchanged.
func (c *Config) resolveLocalEnv() {
	if c == nil {
		return
	}
	for k, v := range c.ContainerEnv {
		c.ContainerEnv[k] = substituteLocalEnv(v)
	}
	for i, s := range c.PostCreateCommand {
		c.PostCreateCommand[i] = substituteLocalEnv(s)
	}
	for i, s := range c.PostStartCommand {
		c.PostStartCommand[i] = substituteLocalEnv(s)
	}
}
