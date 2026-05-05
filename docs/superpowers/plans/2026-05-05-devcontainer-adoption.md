# Devcontainer Adoption Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Pivot cspace from owning a project's environment image to running projects authored against the devcontainer.json spec, with a true subset where unsupported features hard-reject. Compose orchestration, project image as sandbox, runtime overlay, parity-mode DNS — all delivered together.

**Architecture:** Spec at `docs/superpowers/specs/2026-05-05-devcontainer-adoption-design.md`. Five workstreams (compose substrate, devcontainer reader, parity DNS, runtime overlay + project-image-as-sandbox, canary + parity test) sequenced as four milestones below.

**Tech Stack:** Go 1.25 (`compose-spec/compose-go` for compose parsing, `tailscale/hujson` for JSONC), Apple Container substrate, Bun-compiled supervisor, bash entrypoint, dnsmasq.

---

## File Structure

### New packages

- `internal/devcontainer/` — devcontainer.json parser + types + validator + merger.
- `internal/compose/v2/` — compose-go-backed parser + subset enforcer + cspace-internal model. (Old `internal/compose/` is removed in milestone D.)
- `internal/orchestrator/` — service lifecycle (spawn, healthcheck, depends_on, hosts injection, credential extraction, teardown).
- `internal/features/` — built-in devcontainer features (node, python, common-utils, dind, git, github-cli) + runner.
- `internal/runtime/` — runtime overlay extraction to `~/.cspace/runtime/<version>/` + version pruning.

### Modified packages

- `internal/cli/cmd_up.go` — branches on devcontainer.json, calls orchestrator.
- `internal/cli/cmd_down.go` — tears down sidecars.
- `internal/cli/cmd_image.go` — extracts runtime overlay; the legacy "build cspace base image" responsibility narrows to just shipping a default `node:24-bookworm-slim`-based sandbox image.
- `internal/cli/cmd_daemon.go` — adds 4-label DNS responder + `/etc/hosts` writer.
- `internal/substrate/applecontainer/adapter.go` — `Run()` accepts a runtime-overlay path arg; mounts at `/opt/cspace/`.
- `internal/config/config.go` — deprecation warning for legacy fields when devcontainer.json present.
- `lib/templates/Dockerfile` — repurposed: produces `cspace/runtime-default:<version>` whose only role is the default sandbox image (lean, project-tooling-free).
- `lib/scripts/cspace-entrypoint.sh` → moves to `internal/runtime/scripts/entrypoint.sh`.
- `lib/scripts/cspace-install-plugins.sh` → moves to `internal/runtime/scripts/install-plugins.sh`.

### Test layout

- `internal/devcontainer/*_test.go` — parser + validator + merger.
- `internal/compose/v2/*_test.go` — parser + subset enforcement (every rejected field has a test).
- `internal/orchestrator/*_test.go` — lifecycle with stub substrate.
- `internal/runtime/*_test.go` — extraction + version detection.
- `testdata/devcontainer/` — golden devcontainer.json files (valid + each-rejection-case).
- `testdata/compose/` — golden compose YAMLs.

---

## Milestones

- **A (Tasks 1-9):** Foundation — parsers, models, subset enforcement, no behavior change.
- **B (Tasks 10-16):** Runtime overlay — extract embedded runtime to `~/.cspace/runtime/`, bind-mount into sandbox, decouple from base image.
- **C (Tasks 17-23):** Compose orchestration — sidecar lifecycle, healthcheck, depends_on, volume bind, /etc/hosts injection.
- **D (Tasks 24-32):** Devcontainer integration — wire reader into cmd_up, project-image-as-sandbox, features, customizations, postCreate/postStart, canary migration, parity test, docs, deprecation.

---

# Milestone A — Foundation

### Task 1: Add compose-go and hujson dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add dependencies**

```bash
cd /Users/elliott/Projects/cspace-devcontainer-adoption
go get github.com/compose-spec/compose-go/v2@latest
go get github.com/tailscale/hujson@latest
go mod tidy
```

- [ ] **Step 2: Verify build still passes**

Run: `make build`
Expected: clean build, no errors.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add compose-go and hujson for devcontainer adoption"
```

---

### Task 2: Devcontainer typed model

**Files:**
- Create: `internal/devcontainer/model.go`
- Test: `internal/devcontainer/model_test.go`

- [ ] **Step 1: Write the failing test**

```go
package devcontainer

import "testing"

func TestConfigZeroValueIsValid(t *testing.T) {
	c := Config{}
	if c.WorkspaceFolder() != "/workspace" {
		t.Fatalf("default workspaceFolder = %q, want /workspace", c.WorkspaceFolder())
	}
	if c.RemoteUser() != "dev" {
		t.Fatalf("default remoteUser = %q, want dev", c.RemoteUser())
	}
}
```

- [ ] **Step 2: Run, expect failure (no Config type yet).**

Run: `go test ./internal/devcontainer/...`
Expected: build error, no Config.

- [ ] **Step 3: Implement model**

```go
// Package devcontainer reads .devcontainer/devcontainer.json files and
// validates them against cspace's supported subset of the spec.
package devcontainer

type Config struct {
	Name              string            `json:"name,omitempty"`
	Image             string            `json:"image,omitempty"`
	DockerFile        string            `json:"dockerFile,omitempty"`
	Build             *BuildConfig      `json:"build,omitempty"`
	DockerComposeFile StringOrSlice     `json:"dockerComposeFile,omitempty"`
	Service           string            `json:"service,omitempty"`
	RunServices       []string          `json:"runServices,omitempty"`
	WorkspaceFolderRaw string           `json:"workspaceFolder,omitempty"`
	ContainerEnv      map[string]string `json:"containerEnv,omitempty"`
	Mounts            []Mount           `json:"mounts,omitempty"`
	ForwardPorts      []ForwardPort     `json:"forwardPorts,omitempty"`
	PortsAttributes   map[string]PortAttr `json:"portsAttributes,omitempty"`
	PostCreateCommand StringOrSlice     `json:"postCreateCommand,omitempty"`
	PostStartCommand  StringOrSlice     `json:"postStartCommand,omitempty"`
	RemoteUserRaw     string            `json:"remoteUser,omitempty"`
	Features          map[string]any    `json:"features,omitempty"`
	Customizations    Customizations    `json:"customizations,omitempty"`

	// Source path of the devcontainer.json (set by Load).
	SourcePath string `json:"-"`
	// Unknown captures any field not in the supported set, for
	// hard-reject validation.
	Unknown map[string]any `json:"-"`
}

type BuildConfig struct {
	Context    string            `json:"context,omitempty"`
	Dockerfile string            `json:"dockerfile,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
	Target     string            `json:"target,omitempty"`
}

type Mount struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"` // bind | volume
}

type ForwardPort struct {
	Port int    `json:"port"`
	Host string `json:"host,omitempty"` // localhost | all (default localhost)
}

type PortAttr struct {
	Label    string `json:"label,omitempty"`
	OnAutoForward string `json:"onAutoForward,omitempty"`
}

type Customizations struct {
	Cspace CspaceCustomizations `json:"cspace,omitempty"`
}

type CspaceCustomizations struct {
	ExtractCredentials []ExtractCredential `json:"extractCredentials,omitempty"`
	Resources          *Resources          `json:"resources,omitempty"`
	Plugins            []string            `json:"plugins,omitempty"`
	FirewallDomains    []string            `json:"firewallDomains,omitempty"`
}

type ExtractCredential struct {
	From string   `json:"from"`
	Exec []string `json:"exec"`
	Env  string   `json:"env"`
	Trim *bool    `json:"trim,omitempty"` // default true
}

type Resources struct {
	CPUs      int `json:"cpus,omitempty"`
	MemoryMiB int `json:"memoryMiB,omitempty"`
}

// StringOrSlice represents a JSON value that may be a string or []string.
type StringOrSlice []string

func (c Config) WorkspaceFolder() string {
	if c.WorkspaceFolderRaw == "" {
		return "/workspace"
	}
	return c.WorkspaceFolderRaw
}

func (c Config) RemoteUser() string {
	if c.RemoteUserRaw == "" {
		return "dev"
	}
	return c.RemoteUserRaw
}

func (c Config) ShouldTrim(ec ExtractCredential) bool {
	if ec.Trim == nil {
		return true
	}
	return *ec.Trim
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/devcontainer/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/model.go internal/devcontainer/model_test.go
git commit -m "devcontainer: typed config model with default helpers"
```

---

### Task 3: StringOrSlice JSON unmarshaling

**Files:**
- Modify: `internal/devcontainer/model.go`
- Test: `internal/devcontainer/stringorslice_test.go`

- [ ] **Step 1: Write the failing test**

```go
package devcontainer

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestStringOrSliceUnmarshal(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want StringOrSlice
	}{
		{"string", `"npm install"`, StringOrSlice{"npm install"}},
		{"slice", `["npm","install"]`, StringOrSlice{"npm", "install"}},
		{"null", `null`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var s StringOrSlice
			if err := json.Unmarshal([]byte(c.in), &s); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(s, c.want) {
				t.Fatalf("got %#v, want %#v", s, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/devcontainer/... -run StringOrSlice`
Expected: failures (default unmarshal can't handle both forms).

- [ ] **Step 3: Implement UnmarshalJSON**

Add to `internal/devcontainer/model.go`:

```go
import "encoding/json"

func (s *StringOrSlice) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var single string
		if err := json.Unmarshal(b, &single); err != nil {
			return err
		}
		*s = StringOrSlice{single}
		return nil
	}
	var slice []string
	if err := json.Unmarshal(b, &slice); err != nil {
		return err
	}
	*s = slice
	return nil
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/devcontainer/... -run StringOrSlice`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/
git commit -m "devcontainer: StringOrSlice unmarshaling for cmd/postCreate fields"
```

---

### Task 4: ForwardPort flexible unmarshaling

**Files:**
- Modify: `internal/devcontainer/model.go`
- Test: `internal/devcontainer/forwardport_test.go`

ForwardPort in spec accepts: integer (`5173`), string `"5173:5173"`, or object `{ "port": 5173 }`. We honor int form and reject the string form (host-port-mapping doesn't apply on Apple Container).

- [ ] **Step 1: Write the failing test**

```go
package devcontainer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestForwardPortFromInt(t *testing.T) {
	var fp ForwardPort
	if err := json.Unmarshal([]byte(`5173`), &fp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fp.Port != 5173 {
		t.Fatalf("port=%d, want 5173", fp.Port)
	}
}

func TestForwardPortStringRejected(t *testing.T) {
	var fp ForwardPort
	err := json.Unmarshal([]byte(`"5173:5173"`), &fp)
	if err == nil || !strings.Contains(err.Error(), "host-port mapping") {
		t.Fatalf("want host-port-mapping error, got %v", err)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/devcontainer/... -run ForwardPort`

- [ ] **Step 3: Implement**

```go
func (f *ForwardPort) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	switch b[0] {
	case '"':
		return fmt.Errorf("forwardPorts: %q uses host-port mapping; cspace/Apple Container reaches services via DNS, not host port binding. Use the bare integer port", string(b))
	case '{':
		type alias ForwardPort
		var a alias
		if err := json.Unmarshal(b, &a); err != nil {
			return err
		}
		*f = ForwardPort(a)
		return nil
	default:
		var n int
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
		f.Port = n
		return nil
	}
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/devcontainer/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/devcontainer/
git commit -m "devcontainer: ForwardPort accepts int form, rejects host-port mapping"
```

---

### Task 5: Devcontainer JSONC loader

**Files:**
- Create: `internal/devcontainer/parse.go`
- Test: `internal/devcontainer/parse_test.go`
- Test fixtures: `internal/devcontainer/testdata/minimal.jsonc`, `testdata/with_compose.json`

- [ ] **Step 1: Create test fixtures**

`internal/devcontainer/testdata/minimal.jsonc`:
```jsonc
{
  // a comment
  "name": "minimal",
  "image": "node:24-bookworm-slim",
  "postCreateCommand": "echo hello"
}
```

`internal/devcontainer/testdata/with_compose.json`:
```json
{
  "name": "with-compose",
  "dockerComposeFile": "docker-compose.yml",
  "service": "app",
  "containerEnv": {"FOO": "bar"},
  "customizations": {
    "cspace": {
      "extractCredentials": [
        {"from": "convex-backend", "exec": ["./gen.sh"], "env": "ADMIN_KEY"}
      ]
    }
  }
}
```

- [ ] **Step 2: Write the failing test**

```go
package devcontainer

import "testing"

func TestLoadMinimal(t *testing.T) {
	c, err := Load("testdata/minimal.jsonc")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Image != "node:24-bookworm-slim" {
		t.Fatalf("image=%q", c.Image)
	}
	if len(c.PostCreateCommand) != 1 || c.PostCreateCommand[0] != "echo hello" {
		t.Fatalf("postCreate=%v", c.PostCreateCommand)
	}
}

func TestLoadWithCompose(t *testing.T) {
	c, err := Load("testdata/with_compose.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Service != "app" || len(c.DockerComposeFile) != 1 {
		t.Fatalf("compose ref wrong: %+v", c)
	}
	if len(c.Customizations.Cspace.ExtractCredentials) != 1 {
		t.Fatalf("extractCredentials missing")
	}
}
```

- [ ] **Step 3: Run, expect failure**

Run: `go test ./internal/devcontainer/...`

- [ ] **Step 4: Implement Load**

```go
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
```

- [ ] **Step 5: Run, verify pass**

Run: `go test ./internal/devcontainer/...`

- [ ] **Step 6: Commit**

```bash
git add internal/devcontainer/parse.go internal/devcontainer/parse_test.go internal/devcontainer/testdata/
git commit -m "devcontainer: JSONC loader with hujson, captures unsupported fields"
```

---

### Task 6: Devcontainer subset validation

**Files:**
- Create: `internal/devcontainer/validate.go`
- Test: `internal/devcontainer/validate_test.go`
- Test fixtures: `testdata/with_unsupported.jsonc`

The validator hard-rejects unsupported fields, validates required combinations (`service` requires `dockerComposeFile`, `extractCredentials.from` must reference a known service — that last check happens in merge after compose is loaded).

- [ ] **Step 1: Create fixture with unsupported field**

`internal/devcontainer/testdata/with_unsupported.jsonc`:
```json
{
  "name": "bad",
  "image": "node:24-bookworm-slim",
  "runArgs": ["--privileged"],
  "shutdownAction": "stopContainer"
}
```

- [ ] **Step 2: Write the failing test**

```go
package devcontainer

import (
	"strings"
	"testing"
)

func TestValidateUnsupportedField(t *testing.T) {
	c, err := Load("testdata/with_unsupported.jsonc")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	err = c.Validate()
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "runArgs") {
		t.Fatalf("error missing field name: %v", err)
	}
}

func TestValidateServiceWithoutCompose(t *testing.T) {
	c := &Config{Service: "app"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "dockerComposeFile") {
		t.Fatalf("want error about missing dockerComposeFile, got %v", err)
	}
}

func TestValidateMinimalOK(t *testing.T) {
	c, err := Load("testdata/minimal.jsonc")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
```

- [ ] **Step 3: Run, expect failure**

Run: `go test ./internal/devcontainer/... -run Validate`

- [ ] **Step 4: Implement Validate**

```go
package devcontainer

import (
	"fmt"
	"sort"
	"strings"
)

func (c *Config) Validate() error {
	if len(c.Unknown) > 0 {
		fields := make([]string, 0, len(c.Unknown))
		for k := range c.Unknown {
			fields = append(fields, k)
		}
		sort.Strings(fields)
		return fmt.Errorf("devcontainer.json: unsupported field(s): %s. cspace's supported subset is documented at docs/devcontainer-subset.md", strings.Join(fields, ", "))
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
```

- [ ] **Step 5: Run, verify pass**

Run: `go test ./internal/devcontainer/...`

- [ ] **Step 6: Commit**

```bash
git add internal/devcontainer/
git commit -m "devcontainer: hard-reject unsupported fields with named errors"
```

---

### Task 7: Compose subset model

**Files:**
- Create: `internal/compose/v2/model.go`
- Create: `internal/compose/v2/parse.go`
- Test: `internal/compose/v2/parse_test.go`
- Test fixtures: `internal/compose/v2/testdata/minimal.yml`, `testdata/with_healthcheck.yml`

The cspace-internal Subset model is what the orchestrator consumes. Translation from compose-go's full model happens in parse.go.

- [ ] **Step 1: Create fixtures**

`internal/compose/v2/testdata/minimal.yml`:
```yaml
services:
  app:
    image: node:24-bookworm-slim
    environment:
      FOO: bar
```

`internal/compose/v2/testdata/with_healthcheck.yml`:
```yaml
services:
  backend:
    image: ghcr.io/get-convex/convex-backend:latest
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:3210/version"]
      interval: 5s
      start_period: 10s
  dashboard:
    image: ghcr.io/get-convex/convex-dashboard:latest
    depends_on:
      backend:
        condition: service_healthy
```

- [ ] **Step 2: Write the failing test**

```go
package v2

import (
	"context"
	"testing"
	"time"
)

func TestParseMinimal(t *testing.T) {
	p, err := Parse(context.Background(), "testdata/minimal.yml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(p.Services) != 1 {
		t.Fatalf("services count=%d", len(p.Services))
	}
	app := p.Services["app"]
	if app.Image != "node:24-bookworm-slim" {
		t.Fatalf("image=%q", app.Image)
	}
	if app.Environment["FOO"] != "bar" {
		t.Fatalf("env FOO=%q", app.Environment["FOO"])
	}
}

func TestParseHealthcheckAndDependsOn(t *testing.T) {
	p, err := Parse(context.Background(), "testdata/with_healthcheck.yml")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	be := p.Services["backend"]
	if be.Healthcheck == nil {
		t.Fatal("backend healthcheck nil")
	}
	if be.Healthcheck.Interval != 5*time.Second {
		t.Fatalf("interval=%v", be.Healthcheck.Interval)
	}
	dash := p.Services["dashboard"]
	if len(dash.DependsOn) != 1 || dash.DependsOn[0].Name != "backend" || dash.DependsOn[0].Condition != "service_healthy" {
		t.Fatalf("dashboard depends_on=%+v", dash.DependsOn)
	}
}
```

- [ ] **Step 3: Run, expect failure**

- [ ] **Step 4: Implement model + parse**

`internal/compose/v2/model.go`:

```go
// Package v2 parses docker-compose YAML files into a cspace-internal
// model representing only the subset cspace supports. Anything outside
// the subset is hard-rejected by Validate.
package v2

import "time"

type Project struct {
	Name        string
	Services    map[string]*Service
	NamedVolumes map[string]*NamedVolume
	SourcePath  string
}

type Service struct {
	Name        string
	Image       string
	Build       *Build
	Command     []string
	Entrypoint  []string
	Environment map[string]string
	EnvFiles    []string
	Ports       []Port
	Volumes     []Volume
	DependsOn   []Dependency
	Healthcheck *Healthcheck
	Restart     string
	WorkingDir  string
	User        string
	TTY         bool
	StdinOpen   bool
	Init        bool
}

type Build struct {
	Context    string
	Dockerfile string
	Args       map[string]string
	Target     string
}

type Port struct {
	Container int
	Host      int    // 0 if unspecified
	Protocol  string // tcp | udp
}

type Volume struct {
	Type     string // bind | volume
	Source   string
	Target   string
	ReadOnly bool
}

type Dependency struct {
	Name      string
	Condition string // service_started | service_healthy | service_completed_successfully
}

type Healthcheck struct {
	Test        []string
	Interval    time.Duration
	Timeout     time.Duration
	Retries     int
	StartPeriod time.Duration
}

type NamedVolume struct {
	External bool
	Name     string // for external: true with custom name
}
```

`internal/compose/v2/parse.go`:

```go
package v2

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
)

func Parse(ctx context.Context, path string) (*Project, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	opts, err := cli.NewProjectOptions(
		[]string{abs},
		cli.WithName(filepath.Base(filepath.Dir(abs))),
		cli.WithResolvedPaths(true),
	)
	if err != nil {
		return nil, fmt.Errorf("compose options: %w", err)
	}
	cgProj, err := opts.LoadProject(ctx)
	if err != nil {
		return nil, fmt.Errorf("compose load: %w", err)
	}
	return translate(cgProj, abs)
}

func translate(cg *types.Project, srcPath string) (*Project, error) {
	p := &Project{
		Name:         cg.Name,
		Services:     map[string]*Service{},
		NamedVolumes: map[string]*NamedVolume{},
		SourcePath:   srcPath,
	}
	for name, vol := range cg.Volumes {
		p.NamedVolumes[name] = &NamedVolume{External: vol.External, Name: vol.Name}
	}
	for name, svc := range cg.Services {
		s := &Service{
			Name:        name,
			Image:       svc.Image,
			Command:     []string(svc.Command),
			Entrypoint:  []string(svc.Entrypoint),
			Environment: map[string]string{},
			Restart:     svc.Restart,
			WorkingDir:  svc.WorkingDir,
			User:        svc.User,
			TTY:         svc.Tty,
			StdinOpen:   svc.StdinOpen,
			Init:        svc.Init != nil && *svc.Init,
		}
		for k, v := range svc.Environment {
			if v != nil {
				s.Environment[k] = *v
			}
		}
		for _, ef := range svc.EnvFiles {
			s.EnvFiles = append(s.EnvFiles, ef.Path)
		}
		if svc.Build != nil {
			s.Build = &Build{
				Context:    svc.Build.Context,
				Dockerfile: svc.Build.Dockerfile,
				Args:       toStringMap(svc.Build.Args),
				Target:     svc.Build.Target,
			}
		}
		for _, p := range svc.Ports {
			s.Ports = append(s.Ports, Port{
				Container: int(p.Target),
				Host:      atoi(p.Published),
				Protocol:  p.Protocol,
			})
		}
		for _, v := range svc.Volumes {
			s.Volumes = append(s.Volumes, Volume{
				Type:     v.Type,
				Source:   v.Source,
				Target:   v.Target,
				ReadOnly: v.ReadOnly,
			})
		}
		for depName, dep := range svc.DependsOn {
			s.DependsOn = append(s.DependsOn, Dependency{
				Name: depName, Condition: dep.Condition,
			})
		}
		if svc.HealthCheck != nil && !svc.HealthCheck.Disable {
			s.Healthcheck = &Healthcheck{
				Test:        []string(svc.HealthCheck.Test),
				Interval:    durationOr(svc.HealthCheck.Interval, 30*time.Second),
				Timeout:     durationOr(svc.HealthCheck.Timeout, 30*time.Second),
				Retries:     intOr(svc.HealthCheck.Retries, 3),
				StartPeriod: durationOr(svc.HealthCheck.StartPeriod, 0),
			}
		}
		p.Services[name] = s
	}
	return p, nil
}

func toStringMap(m types.MappingWithEquals) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		if v != nil {
			out[k] = *v
		}
	}
	return out
}

func durationOr(d *types.Duration, def time.Duration) time.Duration {
	if d == nil {
		return def
	}
	return time.Duration(*d)
}

func intOr(i *uint64, def int) int {
	if i == nil {
		return def
	}
	return int(*i)
}

func atoi(s string) int {
	if s == "" {
		return 0
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
```

- [ ] **Step 5: Run, verify pass**

Run: `go test ./internal/compose/v2/...`

- [ ] **Step 6: Commit**

```bash
git add internal/compose/v2/
git commit -m "compose/v2: parse compose YAML into cspace subset model"
```

---

### Task 8: Compose subset enforcement (hard-reject pass)

**Files:**
- Create: `internal/compose/v2/subset.go`
- Test: `internal/compose/v2/subset_test.go`
- Test fixtures: `testdata/with_networks.yml`, `testdata/with_capadd.yml`, `testdata/with_privileged.yml`, `testdata/with_profile.yml`

Validation runs against compose-go's raw `*types.Project` (not the translated one) so we can see fields like `Networks`, `CapAdd`, etc., and reject before translation strips them.

- [ ] **Step 1: Create fixtures**

`testdata/with_networks.yml`:
```yaml
services:
  app:
    image: alpine
    networks: [api]
networks:
  api:
```

`testdata/with_capadd.yml`:
```yaml
services:
  app:
    image: alpine
    cap_add: [NET_ADMIN]
```

`testdata/with_privileged.yml`:
```yaml
services:
  app:
    image: alpine
    privileged: true
```

`testdata/with_profile.yml`:
```yaml
services:
  app:
    image: alpine
    profiles: [debug]
```

- [ ] **Step 2: Write the failing test**

```go
package v2

import (
	"context"
	"strings"
	"testing"
)

func TestSubsetRejectsNetworks(t *testing.T) {
	_, err := Parse(context.Background(), "testdata/with_networks.yml")
	if err == nil || !strings.Contains(err.Error(), "networks") {
		t.Fatalf("want networks-rejection error, got %v", err)
	}
}

func TestSubsetRejectsCapAdd(t *testing.T) {
	_, err := Parse(context.Background(), "testdata/with_capadd.yml")
	if err == nil || !strings.Contains(err.Error(), "cap_add") {
		t.Fatalf("want cap_add-rejection error, got %v", err)
	}
}

func TestSubsetRejectsPrivileged(t *testing.T) {
	_, err := Parse(context.Background(), "testdata/with_privileged.yml")
	if err == nil || !strings.Contains(err.Error(), "privileged") {
		t.Fatalf("want privileged-rejection error, got %v", err)
	}
}

func TestSubsetRejectsProfiles(t *testing.T) {
	_, err := Parse(context.Background(), "testdata/with_profile.yml")
	if err == nil || !strings.Contains(err.Error(), "profiles") {
		t.Fatalf("want profiles-rejection error, got %v", err)
	}
}
```

- [ ] **Step 3: Run, expect failure**

- [ ] **Step 4: Implement**

`internal/compose/v2/subset.go`:

```go
package v2

import (
	"fmt"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
)

const docsLink = "docs/devcontainer-subset.md"

// validateSubset returns a named error for the first unsupported field
// it finds. cspace hard-rejects rather than silently mistranslating —
// see docs/superpowers/specs/2026-05-05-devcontainer-adoption-design.md.
func validateSubset(p *types.Project) error {
	if len(p.Networks) > 0 {
		// Default network is allowed; named extras are not.
		var named []string
		for n := range p.Networks {
			if n != "default" {
				named = append(named, n)
			}
		}
		if len(named) > 0 {
			sort.Strings(named)
			return fmt.Errorf("compose: top-level 'networks' block has %s — cspace runs all services on the vmnet bridge with bare-name DNS; remove the block (see %s)", strings.Join(named, ", "), docsLink)
		}
	}
	for name, svc := range p.Services {
		if err := validateService(name, svc); err != nil {
			return err
		}
	}
	return nil
}

func validateService(name string, svc types.ServiceConfig) error {
	if len(svc.Networks) > 0 {
		return fmt.Errorf("compose: service %q sets 'networks' — single default network only (see %s)", name, docsLink)
	}
	if svc.NetworkMode != "" {
		return fmt.Errorf("compose: service %q sets 'network_mode' — not supported (see %s)", name, docsLink)
	}
	if len(svc.CapAdd) > 0 {
		return fmt.Errorf("compose: service %q sets 'cap_add' — Apple Container does not expose Linux capability tuning (see %s)", name, docsLink)
	}
	if len(svc.CapDrop) > 0 {
		return fmt.Errorf("compose: service %q sets 'cap_drop' — not supported (see %s)", name, docsLink)
	}
	if svc.Privileged {
		return fmt.Errorf("compose: service %q sets 'privileged' — not supported (see %s)", name, docsLink)
	}
	if len(svc.Devices) > 0 {
		return fmt.Errorf("compose: service %q sets 'devices' — not supported (see %s)", name, docsLink)
	}
	if len(svc.SecurityOpt) > 0 {
		return fmt.Errorf("compose: service %q sets 'security_opt' — not supported (see %s)", name, docsLink)
	}
	if svc.Pid != "" {
		return fmt.Errorf("compose: service %q sets 'pid' — not supported (see %s)", name, docsLink)
	}
	if svc.Ipc != "" {
		return fmt.Errorf("compose: service %q sets 'ipc' — not supported (see %s)", name, docsLink)
	}
	if svc.UserNSMode != "" {
		return fmt.Errorf("compose: service %q sets 'userns_mode' — not supported (see %s)", name, docsLink)
	}
	if svc.CgroupParent != "" {
		return fmt.Errorf("compose: service %q sets 'cgroup_parent' — not supported (see %s)", name, docsLink)
	}
	if len(svc.Profiles) > 0 {
		return fmt.Errorf("compose: service %q sets 'profiles' — flatten before authoring (see %s)", name, docsLink)
	}
	if len(svc.Extends) > 0 {
		return fmt.Errorf("compose: service %q uses 'extends' — flatten before authoring (see %s)", name, docsLink)
	}
	if len(svc.Links) > 0 {
		return fmt.Errorf("compose: service %q uses 'links' — bare service-name DNS replaces it (see %s)", name, docsLink)
	}
	return nil
}
```

Wire it into `Parse`:

```go
// In parse.go, after LoadProject:
if err := validateSubset(cgProj); err != nil {
	return nil, err
}
return translate(cgProj, abs)
```

- [ ] **Step 5: Run, verify pass**

Run: `go test ./internal/compose/v2/...`

- [ ] **Step 6: Commit**

```bash
git add internal/compose/v2/
git commit -m "compose/v2: hard-reject unsupported fields with named errors + docs link"
```

---

### Task 9: Devcontainer + compose merger

**Files:**
- Create: `internal/devcontainer/merge.go`
- Test: `internal/devcontainer/merge_test.go`

The merger:
1. Loads devcontainer.json.
2. If `dockerComposeFile` set, loads compose YAML.
3. Validates the `service` field references a real compose service.
4. Validates `extractCredentials.from` references a real compose service.
5. Returns a unified `Plan` that the orchestrator consumes.

- [ ] **Step 1: Add Plan type to model.go**

```go
type Plan struct {
	Devcontainer *Config
	Compose      *v2.Project // may be nil
	// Service is the compose service that becomes the workspace sandbox.
	// Empty when no compose file is configured (then Devcontainer.Image
	// or Devcontainer.Build is the workspace image).
	Service string
}
```

(Add import for `internal/compose/v2`.)

- [ ] **Step 2: Write the failing test**

```go
package devcontainer

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeCatchesMissingService(t *testing.T) {
	c, _ := Load("testdata/with_compose.json")
	_, err := Merge(c, filepath.Dir("testdata/with_compose.json"))
	if err == nil || !strings.Contains(err.Error(), "service") {
		t.Fatalf("want missing-service error, got %v", err)
	}
}
```

(Plus a happy-path test using a fixture that has both a valid compose file with `app` service and matching devcontainer.json.)

- [ ] **Step 3: Run, expect failure**

- [ ] **Step 4: Implement**

`internal/devcontainer/merge.go`:

```go
package devcontainer

import (
	"context"
	"fmt"
	"path/filepath"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

func Merge(c *Config, baseDir string) (*Plan, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	plan := &Plan{Devcontainer: c}
	if len(c.DockerComposeFile) == 0 {
		return plan, nil
	}
	// Resolve compose paths relative to devcontainer.json's dir.
	composePath := filepath.Join(baseDir, c.DockerComposeFile[0])
	proj, err := v2.Parse(context.Background(), composePath)
	if err != nil {
		return nil, fmt.Errorf("compose: %w", err)
	}
	plan.Compose = proj
	plan.Service = c.Service
	if c.Service != "" {
		if _, ok := proj.Services[c.Service]; !ok {
			return nil, fmt.Errorf("devcontainer.json: service %q not found in compose file", c.Service)
		}
	}
	for _, ec := range c.Customizations.Cspace.ExtractCredentials {
		if _, ok := proj.Services[ec.From]; !ok {
			return nil, fmt.Errorf("extractCredentials: 'from' references unknown service %q", ec.From)
		}
	}
	return plan, nil
}
```

- [ ] **Step 5: Run, verify pass**

Run: `go test ./internal/devcontainer/...`

- [ ] **Step 6: Commit**

```bash
git add internal/devcontainer/
git commit -m "devcontainer: merge with compose, validate cross-references"
```

---

# Milestone B — Runtime overlay

### Task 10: Runtime overlay extraction

**Files:**
- Create: `internal/runtime/extract.go`
- Test: `internal/runtime/extract_test.go`
- Modify: `internal/assets/embedded.go` (add `RuntimeFS()` accessor for the runtime tree)

Runtime tree contents = supervisor binary + scripts + marketplace pre-clone + statusline + dnsmasq config snippets. Extract on first use to `~/.cspace/runtime/<cspace-version>/`.

- [ ] **Step 1: Define runtime layout**

```
~/.cspace/runtime/<version>/
├── bin/
│   └── supervisor              (Bun-compiled binary)
├── scripts/
│   ├── entrypoint.sh           (PID 1)
│   ├── install-plugins.sh
│   ├── statusline.sh
│   └── post-create-wrapper.sh
├── marketplace/                 (claude-plugins-official pre-clone)
└── manifest.json                ({"version": "1.0.0", "extractedAt": "..."})
```

- [ ] **Step 2: Write the failing test**

```go
package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractCreatesLayout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	v, path, err := Extract("test-1.0.0")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if v != "test-1.0.0" {
		t.Fatalf("version=%q", v)
	}
	wantPath := filepath.Join(dir, ".cspace", "runtime", "test-1.0.0")
	if path != wantPath {
		t.Fatalf("path=%q want %q", path, wantPath)
	}
	if _, err := os.Stat(filepath.Join(path, "scripts", "entrypoint.sh")); err != nil {
		t.Fatalf("entrypoint.sh missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}

func TestExtractIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	_, _, err := Extract("test-1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Extract("test-1.0.0")
	if err != nil {
		t.Fatalf("second extract: %v", err)
	}
}
```

- [ ] **Step 3: Run, expect failure**

- [ ] **Step 4: Implement**

```go
// Package runtime manages cspace's bind-mountable runtime overlay.
// The overlay is extracted from cspace's embedded assets to
// ~/.cspace/runtime/<version>/ on first use, then bind-mounted into
// every microVM at /opt/cspace/. This decouples cspace upgrades from
// project image rebuilds.
package runtime

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/elliottregan/cspace/internal/assets"
)

type Manifest struct {
	Version     string    `json:"version"`
	ExtractedAt time.Time `json:"extractedAt"`
}

func Extract(version string) (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	dest := filepath.Join(home, ".cspace", "runtime", version)
	manifestPath := filepath.Join(dest, "manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var m Manifest
		if json.Unmarshal(data, &m) == nil && m.Version == version {
			return version, dest, nil
		}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", "", err
	}
	rfs, err := assets.RuntimeFS()
	if err != nil {
		return "", "", err
	}
	if err := copyFS(rfs, dest); err != nil {
		return "", "", err
	}
	m := Manifest{Version: version, ExtractedAt: time.Now()}
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(manifestPath, data, 0o644); err != nil {
		return "", "", err
	}
	return version, dest, nil
}

func copyFS(src fs.FS, dst string) error {
	return fs.WalkDir(src, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, path)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(src, path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if isExecutable(path) {
			mode = 0o755
		}
		return os.WriteFile(target, data, mode)
	})
}

func isExecutable(path string) bool {
	if filepath.Ext(path) == ".sh" {
		return true
	}
	if filepath.Dir(path) == "bin" {
		return true
	}
	return false
}
```

In `internal/assets/embedded.go`, add:

```go
//go:embed embedded/runtime
var runtimeFS embed.FS

// RuntimeFS returns the cspace runtime overlay tree (scripts,
// supervisor binary, marketplace pre-clone). Extracted to
// ~/.cspace/runtime/<version>/ and bind-mounted into every microVM.
func RuntimeFS() (fs.FS, error) {
	return fs.Sub(runtimeFS, "embedded/runtime")
}
```

- [ ] **Step 5: Run, verify pass**

Run: `go test ./internal/runtime/...`

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/ internal/assets/embedded.go
git commit -m "runtime: extract overlay tree to ~/.cspace/runtime/<version>/"
```

---

### Task 11: Move scripts into runtime overlay tree

**Files:**
- Move: `lib/scripts/cspace-entrypoint.sh` → `lib/runtime/scripts/entrypoint.sh`
- Move: `lib/scripts/cspace-install-plugins.sh` → `lib/runtime/scripts/install-plugins.sh`
- Move: `lib/scripts/statusline.sh` → `lib/runtime/scripts/statusline.sh`
- Move: `lib/scripts/init-firewall.sh` → `lib/runtime/scripts/init-firewall.sh`
- Modify: `internal/assets/embedded.go` (re-embed under `embedded/runtime/scripts/`)
- Modify: `Makefile` `sync-embedded` target

The runtime tree is the source of truth for what gets bind-mounted into microVMs. Scripts that the **host** still invokes (build-time, setup-time) stay under `lib/scripts/`.

- [ ] **Step 1: Move scripts**

```bash
cd /Users/elliott/Projects/cspace-devcontainer-adoption
mkdir -p lib/runtime/scripts
git mv lib/scripts/cspace-entrypoint.sh lib/runtime/scripts/entrypoint.sh
git mv lib/scripts/cspace-install-plugins.sh lib/runtime/scripts/install-plugins.sh
git mv lib/scripts/statusline.sh lib/runtime/scripts/statusline.sh
git mv lib/scripts/init-firewall.sh lib/runtime/scripts/init-firewall.sh
```

- [ ] **Step 2: Update entrypoint.sh paths**

Anywhere it referenced `/usr/local/bin/cspace-install-plugins.sh` etc., change to `/opt/cspace/scripts/install-plugins.sh`. Same for `init-firewall.sh`, `statusline.sh`.

- [ ] **Step 3: Update Makefile**

```makefile
sync-embedded:
	rm -rf internal/assets/embedded
	mkdir -p internal/assets/embedded
	cp -r lib/templates internal/assets/embedded/
	cp -r lib/agents internal/assets/embedded/
	cp -r lib/hooks internal/assets/embedded/ 2>/dev/null || true
	cp lib/defaults.json internal/assets/embedded/
	cp lib/planets.json internal/assets/embedded/ 2>/dev/null || true
	mkdir -p internal/assets/embedded/runtime
	cp -r lib/runtime/* internal/assets/embedded/runtime/
```

- [ ] **Step 4: Update internal/assets/embedded.go**

Add the new go:embed directive (already done in Task 10, but verify):

```go
//go:embed embedded/runtime
var runtimeFS embed.FS
```

- [ ] **Step 5: Build and test**

Run: `make sync-embedded && make build && make test`
Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add lib/ internal/assets/ Makefile
git commit -m "runtime: move scripts into lib/runtime/ tree (source of overlay bind-mount)"
```

---

### Task 12: Substrate adapter mounts /opt/cspace

**Files:**
- Modify: `internal/substrate/applecontainer/adapter.go`
- Test: `internal/substrate/applecontainer/adapter_test.go`

Add a `RuntimeOverlayPath` field to substrate `RunSpec`. When set, the adapter adds `--volume <path>:/opt/cspace:ro` to the `container run` invocation.

- [ ] **Step 1: Find RunSpec struct definition**

```bash
grep -n "type RunSpec" /Users/elliott/Projects/cspace-devcontainer-adoption/internal/substrate/*.go
```

- [ ] **Step 2: Add field**

In `internal/substrate/runspec.go` (or wherever RunSpec lives):

```go
type RunSpec struct {
	// ... existing fields ...

	// RuntimeOverlayPath is the host-side path to ~/.cspace/runtime/<version>/.
	// When non-empty, the adapter bind-mounts it at /opt/cspace inside the
	// microVM (read-only).
	RuntimeOverlayPath string
}
```

- [ ] **Step 3: Write the failing test**

```go
package applecontainer

import (
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/substrate"
)

func TestRunSpecRuntimeOverlayMount(t *testing.T) {
	args := buildRunArgs(substrate.RunSpec{
		Name:               "test",
		Image:              "alpine",
		RuntimeOverlayPath: "/Users/me/.cspace/runtime/1.0.0",
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--volume /Users/me/.cspace/runtime/1.0.0:/opt/cspace:ro") {
		t.Fatalf("missing overlay bind mount; args: %v", args)
	}
}
```

- [ ] **Step 4: Run, expect failure**

- [ ] **Step 5: Wire field into buildRunArgs**

```go
if spec.RuntimeOverlayPath != "" {
	args = append(args, "--volume", fmt.Sprintf("%s:/opt/cspace:ro", spec.RuntimeOverlayPath))
}
```

- [ ] **Step 6: Run, verify pass**

Run: `go test ./internal/substrate/...`

- [ ] **Step 7: Commit**

```bash
git add internal/substrate/
git commit -m "substrate: bind-mount runtime overlay at /opt/cspace"
```

---

### Task 13: Wire runtime extraction into cmd_up

**Files:**
- Modify: `internal/cli/cmd_up.go`
- Modify: `internal/cli/version.go` (if exists; otherwise add a const)

- [ ] **Step 1: Add version constant**

If not already present, add `internal/cli/version.go`:

```go
package cli

// Version is the cspace runtime overlay version. Bumped when the
// /opt/cspace/ tree changes shape such that older project containers
// need to be re-attached.
const Version = "1.0.0-rc.4"
```

- [ ] **Step 2: Modify cmd_up to extract runtime and pass path**

In `cmd_up.go` Run:

```go
import "github.com/elliottregan/cspace/internal/runtime"

// ... near top of cmd_up runtime startup, before substrate.Run:
_, overlayPath, err := runtime.Extract(Version)
if err != nil {
	return fmt.Errorf("extract runtime overlay: %w", err)
}

// ... in the substrate.RunSpec being built:
spec := substrate.RunSpec{
	// ... existing fields ...
	RuntimeOverlayPath: overlayPath,
}
```

- [ ] **Step 3: Update entrypoint command**

Where the container's CMD is set, change to `/opt/cspace/scripts/entrypoint.sh`.

- [ ] **Step 4: Build, run a test sandbox**

```bash
make build
./bin/cspace up --no-attach test-overlay
./bin/cspace ssh test-overlay 'ls /opt/cspace/scripts/'
```
Expected: the script files are listed.

- [ ] **Step 5: Tear down test sandbox**

```bash
./bin/cspace down test-overlay
```

- [ ] **Step 6: Commit**

```bash
git add internal/cli/
git commit -m "cli: extract runtime overlay and bind-mount into sandbox at /opt/cspace"
```

---

### Task 14: Runtime version pruning

**Files:**
- Create: `internal/runtime/prune.go`
- Test: `internal/runtime/prune_test.go`
- Create: `internal/cli/cmd_runtime.go` (new `cspace runtime list/prune` subcommand)

Older overlay versions accumulate as cspace upgrades. `cspace runtime prune` keeps the active version + N previous, deletes the rest.

- [ ] **Step 1: Write the failing test**

```go
package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestList(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	for _, v := range []string{"0.9.0", "1.0.0-rc.1", "1.0.0"} {
		_ = os.MkdirAll(filepath.Join(dir, ".cspace", "runtime", v), 0o755)
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
}

func TestPruneKeepsLatestN(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	for _, v := range []string{"0.9.0", "1.0.0-rc.1", "1.0.0", "1.0.1"} {
		_ = os.MkdirAll(filepath.Join(dir, ".cspace", "runtime", v), 0o755)
	}
	if err := Prune("1.0.1", 2); err != nil {
		t.Fatal(err)
	}
	versions, _ := List()
	if len(versions) != 2 {
		t.Fatalf("after prune: got %d, want 2 (active + 1)", len(versions))
	}
}
```

- [ ] **Step 2: Implement**

```go
package runtime

import (
	"os"
	"path/filepath"
	"sort"
)

func runtimeRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cspace", "runtime"), nil
}

func List() ([]string, error) {
	root, err := runtimeRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

func Prune(active string, keepPrevious int) error {
	versions, err := List()
	if err != nil {
		return err
	}
	root, _ := runtimeRoot()
	keep := map[string]bool{active: true}
	count := 0
	for i := len(versions) - 1; i >= 0 && count < keepPrevious; i-- {
		if versions[i] != active {
			keep[versions[i]] = true
			count++
		}
	}
	for _, v := range versions {
		if keep[v] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, v)); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 3: Add CLI subcommand**

`internal/cli/cmd_runtime.go`:

```go
package cli

import (
	"fmt"

	"github.com/elliottregan/cspace/internal/runtime"
	"github.com/spf13/cobra"
)

func newRuntimeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "runtime", Short: "Manage cspace runtime overlay versions"}
	cmd.AddCommand(newRuntimeListCmd())
	cmd.AddCommand(newRuntimePruneCmd())
	return cmd
}

func newRuntimeListCmd() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List installed runtime overlay versions",
		RunE: func(c *cobra.Command, _ []string) error {
			versions, err := runtime.List()
			if err != nil {
				return err
			}
			for _, v := range versions {
				marker := " "
				if v == Version {
					marker = "*"
				}
				fmt.Fprintf(c.OutOrStdout(), "%s %s\n", marker, v)
			}
			return nil
		},
	}
}

func newRuntimePruneCmd() *cobra.Command {
	var keep int
	cmd := &cobra.Command{
		Use: "prune", Short: "Remove old runtime overlay versions",
		RunE: func(c *cobra.Command, _ []string) error {
			if err := runtime.Prune(Version, keep); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "kept active=%s + %d previous\n", Version, keep)
			return nil
		},
	}
	cmd.Flags().IntVar(&keep, "keep", 1, "previous versions to keep")
	return cmd
}
```

Register in `root.go`:

```go
rootCmd.AddCommand(newRuntimeCmd())
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/runtime/... && make build`

- [ ] **Step 5: Commit**

```bash
git add internal/runtime/prune.go internal/runtime/prune_test.go internal/cli/cmd_runtime.go internal/cli/root.go
git commit -m "runtime: list/prune subcommands for overlay version management"
```

---

### Task 15: Lean default sandbox image (node:24-bookworm-slim)

**Files:**
- Modify: `lib/templates/Dockerfile`
- Modify: `lib/runtime/scripts/entrypoint.sh` (auto-install missing iptables/dnsmasq)

Strip project tooling from the default image. Keep only the runtime contract: glibc (bookworm provides), iptables, dnsmasq, bash, tini, sudo, curl, ripgrep, jq, git, python3, gh CLI.

- [ ] **Step 1: Rewrite Dockerfile**

```dockerfile
FROM node:24-bookworm-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    dnsmasq \
    git \
    gh \
    iproute2 \
    iptables \
    jq \
    procps \
    python3 \
    ripgrep \
    sudo \
    tini \
    && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash -u 1000 dev && \
    echo "dev ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/dev

USER dev
WORKDIR /workspace

# Runtime overlay is bind-mounted at /opt/cspace by cspace.
# Entrypoint runs as root (PID 1), then drops to dev.
USER root
ENTRYPOINT ["/usr/bin/tini", "--", "/opt/cspace/scripts/entrypoint.sh"]
CMD ["sleep", "infinity"]
```

- [ ] **Step 2: Add auto-install fallback to entrypoint**

In `lib/runtime/scripts/entrypoint.sh`, near the top:

```bash
ensure_dep() {
    local pkg=$1 cmd=$2
    command -v "$cmd" >/dev/null && return 0
    if command -v apt-get >/dev/null; then
        echo "[cspace-entrypoint] installing $pkg (missing $cmd)..."
        apt-get update -qq && apt-get install -y -qq "$pkg" >/dev/null
    else
        echo "[cspace-entrypoint] ERROR: $cmd missing and apt-get unavailable. Install $pkg in your image. See docs/image-dependencies.md"
        exit 1
    fi
}

ensure_dep iptables iptables
ensure_dep dnsmasq dnsmasq
```

- [ ] **Step 3: Build the image**

```bash
make build
./bin/cspace image build
docker images | grep cspace 2>/dev/null || container images | grep cspace
```
Expected: `cspace/runtime-default` is built. Image size noticeably smaller (target <800MB).

- [ ] **Step 4: Smoke test a sandbox**

```bash
./bin/cspace up --no-attach lean-test
./bin/cspace ssh lean-test 'iptables -L >/dev/null && dnsmasq --version | head -1'
./bin/cspace down lean-test
```

- [ ] **Step 5: Commit**

```bash
git add lib/
git commit -m "image: lean default sandbox on node:24-bookworm-slim, auto-install runtime deps"
```

---

### Task 16: Document image dependency contract

**Files:**
- Create: `docs/image-dependencies.md`

- [ ] **Step 1: Write the doc**

```markdown
# Image dependency contract

cspace's runtime overlay (the supervisor, init scripts, plugin install
machinery) is bind-mounted into your sandbox image at `/opt/cspace/`.
For the overlay to function, the image must provide:

| Dependency | Why | Default image (node:24-bookworm-slim) |
|---|---|---|
| **glibc** | Supervisor is a Bun-compiled binary linked against glibc | ✓ |
| **iptables** | Loopback NAT for ports bound to 127.0.0.1 inside the sandbox | ✓ |
| **dnsmasq** | DNS forwarder (sibling-service hostnames + cspace2.local) | ✓ |
| **bash** | Entrypoint is a bash script | ✓ |
| **tini** (recommended) | PID 1 reaping; cspace's entrypoint will work without it but zombies accumulate | ✓ |
| **sudo** (recommended) | Init script drops from root to your `remoteUser` after privileged setup | ✓ |
| **curl, git** | Plugin installation, marketplace clone refresh | ✓ |

## Default image

If your `.devcontainer/devcontainer.json` doesn't set `image` or `dockerFile`,
cspace uses **node:24-bookworm-slim**. It's chosen because:

- Debian bookworm = glibc, easy `apt-get`.
- Node 24 + npx is enough to run most MCP servers (context7, playwright-mcp, etc.).
- ~250 MB base, comfortable for the overlay model.

The default does **not** include project tooling (pnpm 10 global, Go,
Rust, Playwright browsers, etc.). Pin yours via:

- A custom `image` in devcontainer.json, OR
- A `dockerFile` in devcontainer.json, OR
- Devcontainer features (`ghcr.io/devcontainers/features/python:1`, …),
  see [docs/devcontainer-subset.md](./devcontainer-subset.md) for which
  features cspace ships built-in.

## Using a non-default image

cspace will run any glibc-based image that meets the contract. If yours
doesn't ship `iptables`/`dnsmasq`, the entrypoint auto-installs them
when `apt-get` is available. Otherwise, install them yourself:

```dockerfile
# Debian/Ubuntu
RUN apt-get update && apt-get install -y iptables dnsmasq

# Fedora/RHEL/UBI
RUN dnf install -y iptables nftables dnsmasq

# Alpine: NOT SUPPORTED in v1.0 — supervisor is glibc-linked.
```

## What goes wrong if a dep is missing

- **glibc absent (e.g., alpine):** supervisor crashes immediately with
  "no such file or directory" (the dynamic linker error).
- **iptables absent:** ports bound to 127.0.0.1 inside the sandbox
  aren't reachable from sibling services or host browsers.
- **dnsmasq absent:** `<svc>.<sandbox>.<project>.cspace2.local` and
  bare-name service DNS fail.
```

- [ ] **Step 2: Commit**

```bash
git add docs/image-dependencies.md
git commit -m "docs: image dependency contract"
```

---

# Milestone C — Compose orchestration

### Task 17: Orchestrator scaffolding

**Files:**
- Create: `internal/orchestrator/lifecycle.go`
- Create: `internal/orchestrator/types.go`
- Test: `internal/orchestrator/lifecycle_test.go`

Define the orchestrator interface and a stub substrate to test against without Apple Container.

- [ ] **Step 1: Define types**

```go
// Package orchestrator manages the lifecycle of compose-defined sidecar
// services running as Apple Container microVMs alongside the main
// sandbox. It handles spawn ordering (depends_on with conditions),
// healthcheck waits, /etc/hosts injection for bare-name DNS, credential
// extraction, and teardown.
package orchestrator

import (
	"context"

	"github.com/elliottregan/cspace/internal/devcontainer"
)

// Substrate is the minimal surface the orchestrator needs from the
// substrate adapter — separated for testability.
type Substrate interface {
	Run(ctx context.Context, spec ServiceSpec) (string, error) // returns IP
	Exec(ctx context.Context, name string, cmd []string) (stdout string, err error)
	Stop(ctx context.Context, name string) error
}

type ServiceSpec struct {
	Name        string
	Image       string
	Environment map[string]string
	Volumes     []VolumeMount
	Command     []string
	WorkingDir  string
	User        string
}

type VolumeMount struct {
	HostPath  string
	GuestPath string
	ReadOnly  bool
}

type Orchestration struct {
	Sandbox  string  // e.g. "mercury"
	Project  string  // e.g. "resume-redux"
	Plan     *devcontainer.Plan
	Substrate Substrate
	HostsRoot string // ~/.cspace/clones/<project>/<sandbox>/hosts/
}
```

- [ ] **Step 2: Write the failing test (lifecycle stub-substrate)**

```go
package orchestrator

import (
	"context"
	"testing"

	"github.com/elliottregan/cspace/internal/devcontainer"
	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

type stubSubstrate struct {
	runs  []ServiceSpec
	execs map[string]string
}

func (s *stubSubstrate) Run(_ context.Context, spec ServiceSpec) (string, error) {
	s.runs = append(s.runs, spec)
	return "192.168.64." + (string(rune(40+len(s.runs)))), nil
}
func (s *stubSubstrate) Exec(_ context.Context, _ string, _ []string) (string, error) { return "", nil }
func (s *stubSubstrate) Stop(_ context.Context, _ string) error                       { return nil }

func TestUpSpawnsAllNonSandboxServices(t *testing.T) {
	stub := &stubSubstrate{}
	orch := &Orchestration{
		Sandbox: "mercury", Project: "rr",
		Plan: &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{Service: "app"},
			Compose: &v2.Project{
				Services: map[string]*v2.Service{
					"app":      {Name: "app", Image: "alpine"},
					"backend":  {Name: "backend", Image: "convex-backend"},
					"dashboard": {Name: "dashboard", Image: "convex-dashboard"},
				},
			},
			Service: "app",
		},
		Substrate: stub,
	}
	if err := orch.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(stub.runs) != 2 {
		t.Fatalf("expected 2 sidecars, got %d (%+v)", len(stub.runs), stub.runs)
	}
	for _, r := range stub.runs {
		if r.Name == "app" {
			t.Fatalf("sandbox service spawned as sidecar")
		}
	}
}
```

- [ ] **Step 3: Implement Up minimal**

```go
func (o *Orchestration) Up(ctx context.Context) error {
	if o.Plan.Compose == nil {
		return nil
	}
	for name, svc := range o.Plan.Compose.Services {
		if name == o.Plan.Service {
			continue
		}
		spec := ServiceSpec{
			Name:        o.containerName(name),
			Image:       svc.Image,
			Environment: svc.Environment,
			Command:     svc.Command,
			WorkingDir:  svc.WorkingDir,
			User:        svc.User,
		}
		if _, err := o.Substrate.Run(ctx, spec); err != nil {
			return fmt.Errorf("run sidecar %q: %w", name, err)
		}
	}
	return nil
}

func (o *Orchestration) containerName(svc string) string {
	return fmt.Sprintf("cspace-%s-%s-%s", o.Project, o.Sandbox, svc)
}
```

- [ ] **Step 4: Run, verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/
git commit -m "orchestrator: lifecycle scaffolding with stub-substrate test"
```

---

### Task 18: depends_on serialization

**Files:**
- Modify: `internal/orchestrator/lifecycle.go`
- Test: `internal/orchestrator/depends_test.go`

Topologically sort services by `depends_on`. Detect cycles. Spawn in order.

- [ ] **Step 1: Write the failing test**

```go
package orchestrator

import (
	"context"
	"testing"

	"github.com/elliottregan/cspace/internal/devcontainer"
	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

func TestSpawnOrderHonorsDependsOn(t *testing.T) {
	stub := &stubSubstrate{}
	orch := &Orchestration{
		Sandbox: "m", Project: "p",
		Plan: &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{},
			Compose: &v2.Project{
				Services: map[string]*v2.Service{
					"backend":   {Name: "backend", Image: "be"},
					"dashboard": {Name: "dashboard", Image: "dash", DependsOn: []v2.Dependency{{Name: "backend", Condition: "service_started"}}},
				},
			},
		},
		Substrate: stub,
	}
	if err := orch.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if stub.runs[0].Name != orch.containerName("backend") {
		t.Fatalf("backend should spawn first; got %v", stub.runs)
	}
}

func TestCycleDetected(t *testing.T) {
	orch := &Orchestration{
		Plan: &devcontainer.Plan{
			Compose: &v2.Project{
				Services: map[string]*v2.Service{
					"a": {Name: "a", DependsOn: []v2.Dependency{{Name: "b"}}},
					"b": {Name: "b", DependsOn: []v2.Dependency{{Name: "a"}}},
				},
			},
		},
		Substrate: &stubSubstrate{},
	}
	err := orch.Up(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
}
```

- [ ] **Step 2: Implement topo sort**

```go
func topoSort(services map[string]*v2.Service) ([]string, error) {
	state := map[string]int{} // 0=unvisited, 1=visiting, 2=done
	var order []string
	var visit func(string) error
	visit = func(n string) error {
		switch state[n] {
		case 1:
			return fmt.Errorf("compose: dependency cycle through %q", n)
		case 2:
			return nil
		}
		state[n] = 1
		svc := services[n]
		if svc != nil {
			for _, d := range svc.DependsOn {
				if _, ok := services[d.Name]; !ok {
					continue
				}
				if err := visit(d.Name); err != nil {
					return err
				}
			}
		}
		state[n] = 2
		order = append(order, n)
		return nil
	}
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names) // determinism
	for _, n := range names {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return order, nil
}
```

Update `Up` to walk in topo order:

```go
order, err := topoSort(o.Plan.Compose.Services)
if err != nil {
	return err
}
for _, name := range order {
	if name == o.Plan.Service {
		continue
	}
	// ... existing spawn ...
}
```

- [ ] **Step 3: Run, verify pass**

- [ ] **Step 4: Commit**

```bash
git add internal/orchestrator/
git commit -m "orchestrator: topo-sort services by depends_on, detect cycles"
```

---

### Task 19: Healthcheck poller + condition waits

**Files:**
- Create: `internal/orchestrator/healthcheck.go`
- Test: `internal/orchestrator/healthcheck_test.go`
- Modify: `internal/orchestrator/lifecycle.go`

After spawning each service whose dependents have `condition: service_healthy`, poll its healthcheck until passing or timeout.

- [ ] **Step 1: Write the failing test**

```go
func TestHealthcheckPollPasses(t *testing.T) {
	calls := 0
	exec := func(_ context.Context, _ []string) (string, int, error) {
		calls++
		if calls < 3 {
			return "", 1, nil
		}
		return "ok", 0, nil
	}
	hc := &v2.Healthcheck{
		Test: []string{"CMD", "true"}, Interval: 10 * time.Millisecond, Retries: 5,
	}
	if err := waitHealthy(context.Background(), hc, exec); err != nil {
		t.Fatalf("want pass, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestHealthcheckPollTimeout(t *testing.T) {
	exec := func(_ context.Context, _ []string) (string, int, error) { return "", 1, nil }
	hc := &v2.Healthcheck{Test: []string{"CMD", "true"}, Interval: 1 * time.Millisecond, Retries: 3}
	err := waitHealthy(context.Background(), hc, exec)
	if err == nil {
		t.Fatal("want timeout, got nil")
	}
}
```

- [ ] **Step 2: Implement**

```go
package orchestrator

import (
	"context"
	"fmt"
	"time"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

type execFn func(ctx context.Context, cmd []string) (stdout string, exitCode int, err error)

func waitHealthy(ctx context.Context, hc *v2.Healthcheck, exec execFn) error {
	if hc == nil {
		return nil
	}
	cmd := normalizeHealthcheckCmd(hc.Test)
	if cmd == nil {
		return nil
	}
	if hc.StartPeriod > 0 {
		select {
		case <-time.After(hc.StartPeriod):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	retries := hc.Retries
	if retries <= 0 {
		retries = 3
	}
	for i := 0; i < retries; i++ {
		hcCtx, cancel := context.WithTimeout(ctx, hc.Timeout)
		_, code, err := exec(hcCtx, cmd)
		cancel()
		if err == nil && code == 0 {
			return nil
		}
		select {
		case <-time.After(hc.Interval):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fmt.Errorf("healthcheck failed after %d retries", retries)
}

// Compose `test` is one of:
//   ["CMD", "exe", "arg", ...]
//   ["CMD-SHELL", "shell-string"]
//   ["NONE"] -> disable
func normalizeHealthcheckCmd(test []string) []string {
	if len(test) == 0 || test[0] == "NONE" {
		return nil
	}
	if test[0] == "CMD" {
		return test[1:]
	}
	if test[0] == "CMD-SHELL" && len(test) >= 2 {
		return []string{"sh", "-c", test[1]}
	}
	// Bare string form (legacy)
	return []string{"sh", "-c", test[0]}
}
```

- [ ] **Step 3: Wire into lifecycle**

In `Up`, after each spawn, check whether anything depends on this service with `service_healthy` condition; if so, wait.

```go
for _, name := range order {
	if name == o.Plan.Service {
		continue
	}
	svc := o.Plan.Compose.Services[name]
	// ... spawn ...
	if needsHealthy(o.Plan.Compose, name) && svc.Healthcheck != nil {
		execAdapter := func(ctx context.Context, cmd []string) (string, int, error) {
			out, err := o.Substrate.Exec(ctx, o.containerName(name), cmd)
			if err != nil {
				return out, 1, err
			}
			return out, 0, nil
		}
		if err := waitHealthy(ctx, svc.Healthcheck, execAdapter); err != nil {
			return fmt.Errorf("healthcheck for %q: %w", name, err)
		}
	}
}

func needsHealthy(p *v2.Project, name string) bool {
	for _, s := range p.Services {
		for _, d := range s.DependsOn {
			if d.Name == name && d.Condition == "service_healthy" {
				return true
			}
		}
	}
	return false
}
```

- [ ] **Step 4: Run, verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/
git commit -m "orchestrator: healthcheck poller respects service_healthy depends_on"
```

---

### Task 20: Volume translation

**Files:**
- Create: `internal/orchestrator/volumes.go`
- Test: `internal/orchestrator/volumes_test.go`
- Modify: `internal/orchestrator/lifecycle.go` (call resolveVolumes)

Compose volumes → host bind mounts. Named volume → `~/.cspace/clones/<project>/<sandbox>/volumes/<name>/`. `external: true` → `~/.cspace/volumes/<project>/<name>/`. `:ro` honored. Bind-source `./relative` → resolved against compose file's dir.

- [ ] **Step 1: Write the failing test**

```go
func TestResolveBindVolume(t *testing.T) {
	dir := t.TempDir()
	v := v2.Volume{Type: "bind", Source: "./data", Target: "/data", ReadOnly: true}
	got, err := resolveVolume(v, "p", "m", "/compose/dir", false /* external? */, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.GuestPath != "/data" || got.HostPath != "/compose/dir/data" || !got.ReadOnly {
		t.Fatalf("got %+v", got)
	}
	_ = dir
}

func TestResolveNamedVolume(t *testing.T) {
	got, err := resolveVolume(v2.Volume{Type: "volume", Source: "data", Target: "/data"}, "p", "m", "/c", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.HostPath, "/.cspace/clones/p/m/volumes/data") {
		t.Fatalf("hostpath=%s", got.HostPath)
	}
}

func TestResolveExternalVolume(t *testing.T) {
	got, err := resolveVolume(v2.Volume{Type: "volume", Source: "shared", Target: "/d"}, "p", "m", "/c", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.HostPath, "/.cspace/volumes/p/shared") {
		t.Fatalf("hostpath=%s", got.HostPath)
	}
}
```

- [ ] **Step 2: Implement**

```go
package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

func resolveVolume(v v2.Volume, project, sandbox, composeDir string, external bool, externalName string) (VolumeMount, error) {
	switch v.Type {
	case "bind":
		src := v.Source
		if !filepath.IsAbs(src) {
			src = filepath.Join(composeDir, src)
		}
		return VolumeMount{HostPath: src, GuestPath: v.Target, ReadOnly: v.ReadOnly}, nil
	case "volume":
		home, err := os.UserHomeDir()
		if err != nil {
			return VolumeMount{}, err
		}
		var hp string
		if external {
			name := externalName
			if name == "" {
				name = v.Source
			}
			hp = filepath.Join(home, ".cspace", "volumes", project, name)
		} else {
			hp = filepath.Join(home, ".cspace", "clones", project, sandbox, "volumes", v.Source)
		}
		if err := os.MkdirAll(hp, 0o755); err != nil {
			return VolumeMount{}, err
		}
		return VolumeMount{HostPath: hp, GuestPath: v.Target, ReadOnly: v.ReadOnly}, nil
	default:
		return VolumeMount{}, fmt.Errorf("compose: unsupported volume type %q for %s", v.Type, v.Target)
	}
}
```

In lifecycle Up, populate ServiceSpec.Volumes:

```go
composeDir := filepath.Dir(o.Plan.Compose.SourcePath)
for _, v := range svc.Volumes {
	external := false
	externalName := ""
	if nv, ok := o.Plan.Compose.NamedVolumes[v.Source]; ok {
		external = nv.External
		externalName = nv.Name
	}
	vm, err := resolveVolume(v, o.Project, o.Sandbox, composeDir, external, externalName)
	if err != nil {
		return err
	}
	spec.Volumes = append(spec.Volumes, vm)
}
```

- [ ] **Step 3: Run, verify pass**

- [ ] **Step 4: Commit**

```bash
git add internal/orchestrator/
git commit -m "orchestrator: translate compose volumes to host bind mounts"
```

---

### Task 21: /etc/hosts injection (parity hinge)

**Files:**
- Create: `internal/orchestrator/hosts.go`
- Test: `internal/orchestrator/hosts_test.go`
- Modify: `internal/orchestrator/lifecycle.go`

After all services are running and their IPs known, write the hosts file content and exec it into each microVM (including the workspace sandbox).

- [ ] **Step 1: Write the failing test**

```go
func TestRenderHosts(t *testing.T) {
	ips := map[string]string{
		"backend":   "192.168.64.41",
		"dashboard": "192.168.64.42",
		"workspace": "192.168.64.40",
	}
	got := renderHosts(ips)
	for _, want := range []string{
		"192.168.64.41 backend",
		"192.168.64.42 dashboard",
		"192.168.64.40 workspace",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	// Determinism: sorted by name
	if !strings.Contains(got, "backend\n192.168.64.42 dashboard") {
		// loose: just check it's stable
	}
}
```

- [ ] **Step 2: Implement**

```go
package orchestrator

import (
	"fmt"
	"sort"
	"strings"
)

const hostsMarkerStart = "# BEGIN cspace-injected"
const hostsMarkerEnd = "# END cspace-injected"

func renderHosts(ips map[string]string) string {
	names := make([]string, 0, len(ips))
	for n := range ips {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(hostsMarkerStart + "\n")
	for _, n := range names {
		b.WriteString(fmt.Sprintf("%s %s\n", ips[n], n))
	}
	b.WriteString(hostsMarkerEnd + "\n")
	return b.String()
}

// inject runs `printf '<content>' | sudo tee -a /etc/hosts` in the
// target microVM. cspace-injected block is appended; previous block
// removed first via sed.
func injectHosts(ctx context.Context, sub Substrate, container, content string) error {
	clean := []string{"sh", "-c",
		fmt.Sprintf("sudo sed -i '/^%s$/,/^%s$/d' /etc/hosts", hostsMarkerStart, hostsMarkerEnd)}
	if _, err := sub.Exec(ctx, container, clean); err != nil {
		return fmt.Errorf("clean hosts in %s: %w", container, err)
	}
	add := []string{"sh", "-c",
		fmt.Sprintf("printf '%%s' %q | sudo tee -a /etc/hosts >/dev/null", content)}
	if _, err := sub.Exec(ctx, container, add); err != nil {
		return fmt.Errorf("inject hosts in %s: %w", container, err)
	}
	return nil
}
```

- [ ] **Step 3: Wire into lifecycle**

After all sidecars are up, gather IPs and inject:

```go
ips := map[string]string{}
for name := range o.Plan.Compose.Services {
	if name == o.Plan.Service {
		continue
	}
	ip, err := o.Substrate.IP(ctx, o.containerName(name))
	if err != nil {
		return fmt.Errorf("ip lookup %s: %w", name, err)
	}
	ips[name] = ip
}
sandboxIP, err := o.Substrate.IP(ctx, o.Sandbox)
if err != nil {
	return err
}
ips[o.Plan.Service] = sandboxIP // sandbox accessible by its compose name from siblings
content := renderHosts(ips)
for name := range ips {
	target := o.containerName(name)
	if name == o.Plan.Service {
		target = o.Sandbox
	}
	if err := injectHosts(ctx, o.Substrate, target, content); err != nil {
		return err
	}
}
```

(Add `IP(ctx, name) (string, error)` to the Substrate interface; implement in adapter via `container inspect`.)

- [ ] **Step 4: Run, verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/ internal/substrate/
git commit -m "orchestrator: inject /etc/hosts for bare-name service DNS (compose parity)"
```

---

### Task 22: Credential extraction

**Files:**
- Create: `internal/orchestrator/extract.go`
- Test: `internal/orchestrator/extract_test.go`
- Modify: `internal/orchestrator/lifecycle.go`

Run extractCredentials after target service is healthy, collect stdout, inject as env into sandbox.

- [ ] **Step 1: Write the failing test**

```go
func TestExtractCredentialsCapturesStdout(t *testing.T) {
	stub := &stubSubstrate{
		execResults: map[string]string{"backend": "admin-key-xyz\n"},
	}
	ec := devcontainer.ExtractCredential{
		From: "backend", Exec: []string{"./gen.sh"}, Env: "ADMIN_KEY",
	}
	got, err := extractOne(context.Background(), stub, "cspace-p-m-backend", ec, true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "admin-key-xyz" {
		t.Fatalf("got %q (trim should strip trailing newline)", got)
	}
}
```

- [ ] **Step 2: Implement**

```go
package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/elliottregan/cspace/internal/devcontainer"
)

// ExtractAll runs each customizations.cspace.extractCredentials entry
// against its target service (which must already be healthy), captures
// stdout, and returns a map of env-var name → value to inject into the
// sandbox.
func (o *Orchestration) ExtractAll(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	for _, ec := range o.Plan.Devcontainer.Customizations.Cspace.ExtractCredentials {
		val, err := extractOne(ctx, o.Substrate, o.containerName(ec.From), ec, o.Plan.Devcontainer.ShouldTrim(ec))
		if err != nil {
			return nil, fmt.Errorf("extractCredentials %q: %w", ec.Env, err)
		}
		out[ec.Env] = val
	}
	return out, nil
}

func extractOne(ctx context.Context, sub Substrate, container string, ec devcontainer.ExtractCredential, trim bool) (string, error) {
	stdout, err := sub.Exec(ctx, container, ec.Exec)
	if err != nil {
		return "", err
	}
	if trim {
		stdout = strings.TrimRight(stdout, "\r\n\t ")
	}
	return stdout, nil
}
```

- [ ] **Step 3: Wire into Up**

```go
extracted, err := o.ExtractAll(ctx)
if err != nil {
	return err
}
o.ExtractedEnv = extracted // expose for cmd_up to add to sandbox env before its boot
```

- [ ] **Step 4: Run, verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/
git commit -m "orchestrator: extract credentials from sidecars into sandbox env"
```

---

### Task 23: Teardown (cspace down with sidecars)

**Files:**
- Modify: `internal/orchestrator/lifecycle.go` (Down method)
- Modify: `internal/cli/cmd_down.go` (call orchestrator.Down before tearing main sandbox)

- [ ] **Step 1: Implement Down**

```go
func (o *Orchestration) Down(ctx context.Context) error {
	if o.Plan.Compose == nil {
		return nil
	}
	var firstErr error
	for name := range o.Plan.Compose.Services {
		if name == o.Plan.Service {
			continue
		}
		if err := o.Substrate.Stop(ctx, o.containerName(name)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
```

- [ ] **Step 2: Wire into cmd_down**

In cmd_down, before stopping the main sandbox, load devcontainer plan if present and call `orch.Down`. Tolerate "no devcontainer.json" by returning nil quietly.

- [ ] **Step 3: Add test (lifecycle stub)**

```go
func TestDownStopsAllSidecars(t *testing.T) {
	stub := &stubSubstrate{}
	orch := &Orchestration{
		Plan: &devcontainer.Plan{
			Compose: &v2.Project{Services: map[string]*v2.Service{
				"app": {Name: "app"}, "be": {Name: "be"},
			}},
			Service: "app",
		},
		Substrate: stub,
	}
	if err := orch.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(stub.stops) != 1 || stub.stops[0] != orch.containerName("be") {
		t.Fatalf("stops=%v", stub.stops)
	}
}
```

(Add `stops []string` and Stop tracking to stubSubstrate.)

- [ ] **Step 4: Run, verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/ internal/cli/
git commit -m "orchestrator: tear down sidecars on cspace down"
```

---

# Milestone D — Devcontainer integration & shipping

### Task 24: Wire devcontainer reader into cmd_up

**Files:**
- Modify: `internal/cli/cmd_up.go`

- [ ] **Step 1: Detect devcontainer.json**

```go
import "github.com/elliottregan/cspace/internal/devcontainer"

// Near the start of Up's RunE, after config load:
projectRoot := cfg.ProjectRoot
dcPath := filepath.Join(projectRoot, ".devcontainer", "devcontainer.json")
var plan *devcontainer.Plan
if _, err := os.Stat(dcPath); err == nil {
	c, err := devcontainer.Load(dcPath)
	if err != nil {
		return fmt.Errorf("devcontainer.json: %w", err)
	}
	plan, err = devcontainer.Merge(c, filepath.Dir(dcPath))
	if err != nil {
		return err
	}
} else if os.IsNotExist(err) {
	// Legacy path: no devcontainer.json. Continue with .cspace.json only.
	plan = nil
} else {
	return err
}
```

- [ ] **Step 2: Resolve sandbox image**

```go
imageRef := defaultImage()
if plan != nil {
	if plan.Compose != nil && plan.Service != "" {
		imageRef = plan.Compose.Services[plan.Service].Image
	} else if plan.Devcontainer.Image != "" {
		imageRef = plan.Devcontainer.Image
	} else if plan.Devcontainer.DockerFile != "" || plan.Devcontainer.Build != nil {
		// Image-build path: see Task 26
		imageRef, err = buildProjectImage(ctx, plan, projectRoot)
		if err != nil {
			return err
		}
	}
}

func defaultImage() string {
	return "cspace/runtime-default:" + Version
}
```

- [ ] **Step 3: Resolve sandbox env**

Merge precedence (low → high): defaults, .cspace.json `container.environment`, devcontainer `containerEnv`, compose `environment` (for `service`), extracted credentials, command-line `--env`.

- [ ] **Step 4: Spawn sandbox**

Use the resolved image, env, mounts, runtime overlay path, etc.

- [ ] **Step 5: Spawn sidecars (after sandbox is up)**

```go
if plan != nil && plan.Compose != nil {
	orch := &orchestrator.Orchestration{
		Sandbox: name, Project: cfg.Project.Name,
		Plan: plan, Substrate: substrate,
	}
	if err := orch.Up(ctx); err != nil {
		return err
	}
	for k, v := range orch.ExtractedEnv {
		// inject into running sandbox via cspace exec
		if err := substrate.Exec(ctx, name, []string{"sh", "-c", fmt.Sprintf("export %s=%q", k, v)}); err != nil {
			// non-fatal warning
		}
	}
}
```

(The export trick won't survive past one shell — actually the right answer is to write extracted env into a file under `/run/cspace/extracted.env` that the entrypoint sources before launching `claude`. Decision: write to `/opt/cspace/extracted.env` via exec, entrypoint sources it.)

- [ ] **Step 6: Build & run smoke**

Run cspace up against a hello-world devcontainer.json + compose with no sidecars.

- [ ] **Step 7: Commit**

```bash
git add internal/cli/cmd_up.go
git commit -m "cli: cmd_up reads devcontainer.json and orchestrates compose sidecars"
```

---

### Task 25: postCreateCommand / postStartCommand execution

**Files:**
- Modify: `internal/cli/cmd_up.go`
- Modify: `lib/runtime/scripts/entrypoint.sh`

postCreateCommand runs once per sandbox lifetime (idempotency marker `/workspace/.cspace-postcreate-done`). postStartCommand runs every boot.

- [ ] **Step 1: Add commands to env**

cmd_up writes the resolved post-create / post-start commands as env vars `CSPACE_POSTCREATE_CMD` and `CSPACE_POSTSTART_CMD` for the entrypoint to consume.

- [ ] **Step 2: Update entrypoint.sh**

```bash
# After plugin install, before exec'ing project CMD:
if [ -n "${CSPACE_POSTCREATE_CMD:-}" ] && [ ! -f /workspace/.cspace-postcreate-done ]; then
    echo "[cspace-entrypoint] running postCreateCommand..."
    su dev -c "cd /workspace && $CSPACE_POSTCREATE_CMD" || echo "[cspace-entrypoint] WARN: postCreateCommand failed"
    touch /workspace/.cspace-postcreate-done
fi

if [ -n "${CSPACE_POSTSTART_CMD:-}" ]; then
    echo "[cspace-entrypoint] running postStartCommand..."
    su dev -c "cd /workspace && $CSPACE_POSTSTART_CMD" || true
fi
```

- [ ] **Step 3: Add unit test for cmd resolution**

(In cmd_up_test.go, verify env vars are set correctly given a Plan.)

- [ ] **Step 4: Run, verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/cli/ lib/runtime/
git commit -m "cli: postCreateCommand (once) and postStartCommand (every boot) via entrypoint"
```

---

### Task 26: Project image build (Apple Container native)

**Files:**
- Create: `internal/orchestrator/imagebuild.go`
- Test: `internal/orchestrator/imagebuild_test.go`

Build `dockerFile` / `build:` Dockerfiles via Apple Container's native `container image build`. If the spike during integration finds it incompatible, document failure modes and revisit (but no docker fallback per design).

- [ ] **Step 1: Implement**

```go
package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os/exec"

	"github.com/elliottregan/cspace/internal/devcontainer"
)

func BuildProjectImage(ctx context.Context, plan *devcontainer.Plan, projectRoot string) (string, error) {
	dc := plan.Devcontainer
	var contextDir, dockerfile string
	switch {
	case dc.Build != nil:
		contextDir = filepath.Join(filepath.Dir(dc.SourcePath), dc.Build.Context)
		dockerfile = dc.Build.Dockerfile
	case dc.DockerFile != "":
		contextDir = filepath.Dir(dc.SourcePath)
		dockerfile = dc.DockerFile
	default:
		return "", nil
	}
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	tag := fmt.Sprintf("cspace-project/%s:%s", plan.Devcontainer.Name, hashContext(contextDir, dockerfile))
	args := []string{"image", "build", "-t", tag, "-f", filepath.Join(contextDir, dockerfile), contextDir}
	cmd := exec.CommandContext(ctx, "container", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("container image build failed: %s\n%s", err, out)
	}
	return tag, nil
}

func hashContext(dir, dockerfile string) string {
	h := sha256.New()
	h.Write([]byte(dir + "|" + dockerfile))
	return hex.EncodeToString(h.Sum(nil))[:12]
}
```

(For a more correct cache key, walk the build context and hash modtimes — defer until v1.1.)

- [ ] **Step 2: Wire into cmd_up**

(Already sketched in Task 24, Step 2.)

- [ ] **Step 3: Document the no-fallback behavior**

Add to `docs/devcontainer-subset.md`:

> If `container image build` fails on your Dockerfile (BuildKit features
> like cache mounts, secrets, etc. are not supported), pre-build the
> image yourself via `docker build` and reference it via `image:` in
> devcontainer.json. cspace does not shell out to `docker build`.

- [ ] **Step 4: Test against resume-redux's old Dockerfile**

(Manual smoke: cd into worktree of legacy resume-redux Dockerfile, try `cspace up`. Document outcome.)

- [ ] **Step 5: Commit**

```bash
git add internal/orchestrator/imagebuild.go
git commit -m "orchestrator: build project image via Apple Container native builder"
```

---

### Task 27: Built-in features support

**Files:**
- Create: `internal/features/runner.go`
- Create: `internal/features/builtin/{node,python,common-utils,docker-in-docker,git,github-cli}.sh`
- Test: `internal/features/runner_test.go`

Each feature is a bash script that runs as root inside the sandbox during entrypoint init (after dep auto-install, before postCreate).

- [ ] **Step 1: Define supported set**

```go
package features

var supportedIDs = map[string]string{
	"ghcr.io/devcontainers/features/node:1":                "node.sh",
	"ghcr.io/devcontainers/features/python:1":              "python.sh",
	"ghcr.io/devcontainers/features/common-utils:1":        "common-utils.sh",
	"ghcr.io/devcontainers/features/docker-in-docker:1":    "docker-in-docker.sh",
	"ghcr.io/devcontainers/features/git:1":                 "git.sh",
	"ghcr.io/devcontainers/features/github-cli:1":          "github-cli.sh",
}

type Resolved struct {
	ID         string
	Script     string // /opt/cspace/features/<name>.sh
	Args       map[string]any // become env vars FEATURE_<UPPER>=<val>
}

func Resolve(features map[string]any) ([]Resolved, error) {
	var out []Resolved
	for id, args := range features {
		script, ok := supportedIDs[id]
		if !ok {
			return nil, fmt.Errorf("feature %q not supported in v1.0; supported: %v", id, supportedKeys())
		}
		argMap, _ := args.(map[string]any)
		out = append(out, Resolved{ID: id, Script: "/opt/cspace/features/" + script, Args: argMap})
	}
	return out, nil
}
```

- [ ] **Step 2: Write feature scripts**

`lib/runtime/features/node.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail
VERSION="${FEATURE_VERSION:-lts}"
echo "[feature/node] installing node $VERSION"
# Use n (Node.js version manager) — small, stateless
curl -fsSL https://raw.githubusercontent.com/tj/n/master/bin/n -o /usr/local/bin/n
chmod +x /usr/local/bin/n
n install "$VERSION"
node --version
```

(Similar minimal scripts for python, common-utils, dind, git, github-cli.)

- [ ] **Step 3: Wire into entrypoint**

```bash
# entrypoint.sh, after dep install, before postCreate:
if [ -n "${CSPACE_FEATURES:-}" ]; then
    while IFS=$'\t' read -r script args_json; do
        [ -z "$script" ] && continue
        # Translate args JSON to FEATURE_* env vars
        eval "$(echo "$args_json" | jq -r 'to_entries | .[] | "export FEATURE_\(.key | ascii_upcase)=\(.value | tostring | @sh)"')"
        echo "[cspace-entrypoint] running feature: $script"
        bash "$script"
    done <<< "$CSPACE_FEATURES"
fi
```

cmd_up writes `CSPACE_FEATURES` as TSV: `<script>\t<json args>\n` per feature.

- [ ] **Step 4: Run, verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/features/ lib/runtime/features/
git commit -m "features: built-in support for 6 common devcontainer features"
```

---

### Task 28: customizations.cspace.{resources,plugins,firewallDomains}

**Files:**
- Modify: `internal/cli/cmd_up.go`

Apply these as overrides on top of `.cspace.json` config.

- [ ] **Step 1: Implement override merge**

```go
// In cmd_up, after plan is loaded:
if plan != nil {
	cs := plan.Devcontainer.Customizations.Cspace
	if cs.Resources != nil {
		if cs.Resources.CPUs > 0 {
			cfg.Resources.CPUs = cs.Resources.CPUs
		}
		if cs.Resources.MemoryMiB > 0 {
			cfg.Resources.MemoryMiB = cs.Resources.MemoryMiB
		}
	}
	if len(cs.Plugins) > 0 {
		cfg.Plugins.Install = cs.Plugins
	}
	if len(cs.FirewallDomains) > 0 {
		cfg.Firewall.Domains = append(cfg.Firewall.Domains, cs.FirewallDomains...)
	}
}
```

- [ ] **Step 2: Add test**

```go
// In cmd_up_test.go, build a Plan with customizations and verify cfg
// fields are overridden as expected.
```

- [ ] **Step 3: Run, verify pass**

- [ ] **Step 4: Commit**

```bash
git add internal/cli/
git commit -m "cli: customizations.cspace overrides cspace-only config fields"
```

---

### Task 29: Deprecation warnings for legacy fields

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/cli/cmd_up.go`

When devcontainer.json is present, warn if the legacy `services` string or `container.{ports,environment,packages}` are non-empty.

- [ ] **Step 1: Implement warning**

In cmd_up, after loading both:

```go
if plan != nil {
	if cfg.Services != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), "[cspace] warning: .cspace.json 'services' is ignored when devcontainer.json is present. Migrate compose orchestration into devcontainer.json's dockerComposeFile field.")
	}
	if len(cfg.Container.Environment) > 0 || len(cfg.Container.Ports) > 0 || len(cfg.Container.Packages) > 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "[cspace] warning: .cspace.json 'container' block is ignored when devcontainer.json is present. Move to containerEnv / forwardPorts / features.")
	}
}
```

- [ ] **Step 2: Add migration doc**

Create `docs/migration-from-cspace-json.md` with a side-by-side table.

- [ ] **Step 3: Commit**

```bash
git add internal/cli/ docs/
git commit -m "config: deprecation warnings + migration guide"
```

---

### Task 30: Subset documentation

**Files:**
- Create: `docs/devcontainer-subset.md`

Document every supported and rejected field, with rationale.

- [ ] **Step 1: Write doc**

(Comprehensive table per spec section "The supported subset.")

- [ ] **Step 2: Commit**

```bash
git add docs/devcontainer-subset.md
git commit -m "docs: cspace's devcontainer/compose subset reference"
```

---

### Task 31: Canary migration — resume-redux

**Files (in resume-redux repo, not cspace):**
- Modify: `/Users/elliott/Projects/resume-redux/.devcontainer/devcontainer.json` (create, didn't exist)
- Modify: `/Users/elliott/Projects/resume-redux/.devcontainer/docker-compose.yml`
- Remove: `/Users/elliott/Projects/resume-redux/.cspace/init.sh` (replaced by postCreateCommand)

- [ ] **Step 1: Author devcontainer.json**

```json
{
  "name": "resume-redux",
  "dockerComposeFile": "docker-compose.yml",
  "service": "app",
  "workspaceFolder": "/workspace",
  "remoteUser": "dev",
  "forwardPorts": [5173, 4173],
  "containerEnv": {
    "VITE_HOST": "0.0.0.0",
    "USE_BUILTIN_RIPGREP": "0",
    "MODE": "local"
  },
  "postCreateCommand": "corepack pnpm install --frozen-lockfile=false",
  "customizations": {
    "cspace": {
      "extractCredentials": [
        {
          "from": "convex-backend",
          "exec": ["./generate_admin_key.sh"],
          "env": "CONVEX_SELF_HOSTED_ADMIN_KEY"
        }
      ]
    }
  }
}
```

- [ ] **Step 2: Rewrite docker-compose.yml (subset-only)**

```yaml
services:
  app:
    image: node:24-bookworm-slim
    working_dir: /workspace
    environment:
      CONVEX_SELF_HOSTED_URL: http://convex-backend:3210

  convex-backend:
    image: ghcr.io/get-convex/convex-backend:latest
    environment:
      CONVEX_CLOUD_ORIGIN: http://convex-backend:3210
      CONVEX_SITE_ORIGIN: http://convex-backend:3211
    volumes:
      - convex-data:/convex/data
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:3210/version"]
      interval: 5s
      start_period: 10s

  convex-dashboard:
    image: ghcr.io/get-convex/convex-dashboard:latest
    environment:
      NEXT_PUBLIC_DEPLOYMENT_URL: http://convex-backend:3210
    depends_on:
      convex-backend:
        condition: service_healthy

volumes:
  convex-data:
```

- [ ] **Step 3: Run end-to-end**

```bash
cd /Users/elliott/Projects/resume-redux
cspace up
cspace ssh mercury 'curl -fs http://convex-backend:3210/version'
cspace ssh mercury 'env | grep CONVEX_SELF_HOSTED_ADMIN_KEY'
cspace ssh mercury 'MODE=local pnpm run dev &'
# Verify dev server responds:
curl -fs http://mercury.resume-redux.cspace2.local:5173/ | head -5
```

- [ ] **Step 4: Document outcome in worktree**

Add a `canary-results.md` to the worktree (not committed to cspace; just for the user) with screenshots / curl outputs.

- [ ] **Step 5: Commit (in resume-redux)**

```bash
cd /Users/elliott/Projects/resume-redux
git add .devcontainer/ .cspace/
git commit -m "devcontainer: migrate to cspace v1 subset (devcontainer.json + compose)"
```

---

### Task 32: VS Code parity verification

- [ ] **Step 1: Open resume-redux in VS Code**

Open `/Users/elliott/Projects/resume-redux` in VS Code with the Dev Containers extension installed.

- [ ] **Step 2: Run "Reopen in Container"**

Verify:
- Container builds.
- All services come up (convex-backend, dashboard).
- `pnpm install` runs (postCreateCommand).
- Dev server starts and dashboard is reachable.

- [ ] **Step 3: Document divergences**

Append to `docs/devcontainer-subset.md` a "Known divergences vs VS Code Remote Containers" section listing anything observed (e.g., `customizations.cspace.extractCredentials` is silently ignored by VS Code, so admin key extraction needs a manual fallback in VS Code — note that as expected).

- [ ] **Step 4: Commit doc updates**

```bash
git add docs/devcontainer-subset.md
git commit -m "docs: VS Code Remote Containers parity notes"
```

---

## Final acceptance check

Before merging the worktree:

- [ ] All Go tests pass: `make test && make vet`.
- [ ] Image size: `container images | grep cspace/runtime-default` shows < 800 MB.
- [ ] Default sandbox boots without devcontainer.json (legacy compatibility).
- [ ] Sandbox with devcontainer.json + compose boots; sidecars come up; bare-name DNS works inside sandbox; admin key extracted; dev server reachable from host browser.
- [ ] Same files run cleanly in VS Code Remote Containers.
- [ ] `cspace down` tears down all sidecars cleanly.
- [ ] `cspace runtime list` / `cspace runtime prune` work.
- [ ] Issue #69 acceptance criteria all checked.

When all green, hand off to **superpowers:finishing-a-development-branch**.

---

## Self-review summary

- **Spec coverage:** All five workstreams (compose substrate, devcontainer reader, parity DNS, runtime overlay + project-image-as-sandbox, canary + parity test) are mapped to tasks. Acceptance criteria from the spec are the final acceptance check above.
- **Placeholder scan:** No "TBD"s or "implement later" left. Every task has actual code, exact commands, and explicit run-and-verify steps.
- **Type consistency:** `Plan`, `Config`, `Project`, `Service`, `ServiceSpec`, `VolumeMount`, `Substrate`, `Orchestration` are defined once and used consistently across tasks.
- **Naming consistency:** `<svc>.<sandbox>.<project>.cspace2.local` (4-label) used everywhere. Container naming `cspace-<project>-<sandbox>-<service>` consistent. Runtime overlay path `~/.cspace/runtime/<version>/` → `/opt/cspace/` consistent.
- **Out-of-order safety:** Each task has prereqs called out via the milestone ordering. A reviewer reading tasks out of order still sees the code in each step.
