package devcontainer

import (
	"fmt"
	"sort"
	"strings"
)

const subsetDocsLink = "docs/devcontainer-subset.md"

// Validate enforces cspace's supported subset of the devcontainer.json
// spec. Unknown fields hard-reject with a named error pointing to the
// subset docs, so unsupported features cannot be silently mistranslated.
func (c *Config) Validate() error {
	if len(c.Unknown) > 0 {
		fields := make([]string, 0, len(c.Unknown))
		for k := range c.Unknown {
			fields = append(fields, k)
		}
		sort.Strings(fields)
		return fmt.Errorf("devcontainer.json: unsupported field(s): %s. cspace's supported subset is documented at %s", strings.Join(fields, ", "), subsetDocsLink)
	}
	if c.Service != "" && len(c.DockerComposeFile) == 0 {
		return fmt.Errorf("devcontainer.json: 'service' set but 'dockerComposeFile' is missing")
	}
	if c.Image != "" && c.DockerFile != "" {
		return fmt.Errorf("devcontainer.json: 'image' and 'dockerFile' are mutually exclusive")
	}
	if c.Build != nil && c.DockerFile != "" {
		return fmt.Errorf("devcontainer.json: 'build' and 'dockerFile' are mutually exclusive")
	}
	for _, ec := range c.Customizations.Cspace.ExtractCredentials {
		if ec.From == "" || len(ec.Exec) == 0 || ec.Env == "" {
			return fmt.Errorf("devcontainer.json: extractCredentials entry requires 'from', 'exec', and 'env'")
		}
	}
	return nil
}
