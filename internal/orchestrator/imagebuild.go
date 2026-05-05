package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/elliottregan/cspace/internal/devcontainer"
)

// BuildProjectImage builds the project's image when devcontainer.json
// sets `dockerFile` or `build:`. Uses Apple Container's native builder
// (`container image build`). Returns the resulting image tag.
//
// Returns an empty string and nil error when no build is needed (the
// devcontainer.json sets `image:` directly or no devcontainer is present).
//
// Does NOT shell out to docker. If the Apple Container builder rejects
// the Dockerfile (e.g., BuildKit cache-mount features), the project must
// pre-build elsewhere and reference via `image:` — documented in
// docs/devcontainer-subset.md.
func BuildProjectImage(ctx context.Context, plan *devcontainer.Plan) (string, error) {
	if plan == nil || plan.Devcontainer == nil {
		return "", nil
	}
	dc := plan.Devcontainer
	srcDir := filepath.Dir(dc.SourcePath)
	var contextDir, dockerfile string
	switch {
	case dc.Build != nil:
		contextDir = dc.Build.Context
		if contextDir == "" {
			contextDir = "."
		}
		if !filepath.IsAbs(contextDir) {
			contextDir = filepath.Join(srcDir, contextDir)
		}
		dockerfile = dc.Build.Dockerfile
	case dc.DockerFile != "":
		contextDir = srcDir
		dockerfile = dc.DockerFile
	default:
		return "", nil
	}
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	dockerfilePath := dockerfile
	if !filepath.IsAbs(dockerfilePath) {
		dockerfilePath = filepath.Join(contextDir, dockerfile)
	}

	tag := fmt.Sprintf("cspace-project/%s:%s", projectImageName(dc.Name), hashContext(contextDir, dockerfile))
	args := []string{"image", "build", "-t", tag, "-f", dockerfilePath, contextDir}
	cmd := exec.CommandContext(ctx, "container", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("container image build failed (%s): %s\n%s", err, tag, out)
	}
	return tag, nil
}

// projectImageName turns a possibly-empty devcontainer name into a tag-safe
// component. Empty → "unnamed".
func projectImageName(name string) string {
	if name == "" {
		return "unnamed"
	}
	// Tag components: a-z 0-9 - . _; lowercase. Replace anything else with -.
	var b []byte
	for _, r := range name {
		switch {
		case r >= 'A' && r <= 'Z':
			b = append(b, byte(r-'A'+'a'))
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			b = append(b, byte(r))
		default:
			b = append(b, '-')
		}
	}
	if len(b) == 0 {
		return "unnamed"
	}
	return string(b)
}

// hashContext produces a short cache key for the build args. v1.0 only
// hashes the context directory path + dockerfile name — full content
// hashing is a v1.1 follow-up. This means edits to a Dockerfile DON'T
// invalidate the image tag automatically; users wanting a rebuild can
// pass `cspace up --rebuild` (separate flag) or change a path.
func hashContext(dir, dockerfile string) string {
	h := sha256.New()
	h.Write([]byte(dir + "|" + dockerfile))
	return hex.EncodeToString(h.Sum(nil))[:12]
}
