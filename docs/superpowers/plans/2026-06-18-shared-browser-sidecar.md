# Shared Browser Sidecar (Phase 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the per-instance browser sidecar with one shared, ref-counted browser per project (`cspace-<project>-browser`), routing per-instance reachability through the existing `.cspace.test` DNS instead of bare-name `/etc/hosts` injection — with a `--no-shared-browser` opt-out.

**Architecture:** Add a singleton container name + a friendly-host helper; reuse the existing per-instance run logic via a non-torching `runBrowserSidecar`; add `ensureSharedBrowserSidecar` (start-if-absent / reuse-if-healthy). `cspace up` calls it when sharing is on, sets `CSPACE_WORKSPACE_HOST` to the per-instance FQDN, and drops the browser-sidecar `/etc/hosts` injection. `cspace down` ref-counts the project's registry entries and stops the singleton only when the last instance is gone. The DNS daemon already resolves `<sandbox>.<project>.cspace.test` → the instance IP (no daemon change).

**Tech Stack:** Go (`internal/cli`, `internal/config`, `internal/registry`); Apple Container CLI (`container run/inspect/stop/rm`); the cspace DNS daemon (`cmd_daemon.go`, unchanged).

## Global Constraints

- **Singleton name:** `cspace-<project>-browser` (`fmt.Sprintf("cspace-%s-browser", project)`). The per-instance name stays `cspace-<project>-<sandbox>-browser`.
- **Friendly workspace host:** `strings.ToLower(name) + "." + strings.ToLower(project) + ".cspace.test"` — must lowercase both labels (the daemon lowercases the query and compares case-sensitively against registry keys; cspace names are already lowercase).
- **Shared is the default.** Opt out via `--no-shared-browser` (flag) or `.cspace.json` `browser.shared: false`; the **flag overrides config** (flag applied last, mirroring `resolveBrowserEnabled`).
- **Never torch a healthy shared singleton.** Reuse it. A singleton is only stopped+restarted when it is missing, unhealthy (CDP doesn't answer), or version-mismatched. Per-instance teardown must not kill a shared singleton.
- **No daemon change, no plugin change.** Phase 2 changes only `internal/cli/browser.go`, `internal/cli/cmd_up.go`, `internal/cli/cmd_down.go`, `internal/registry/registry.go`, `internal/config/config.go` (+ tests).
- **`BrowserSidecar` struct (browser.go:66-71):** `ContainerName, IP, CDPURL, RunServerWSURL string`. CDP/WS URLs are `http://<ip>:9222` and `ws://<ip>:3000/` (`browserCDPPort=9222`, `browserRunServerPort=3000`).
- The sidecar IP is vmnet-assigned and changes on every (re)start — always re-`waitForBrowserIP` on reuse; never cache a prior CDP URL.

---

### Task 1: Pure helpers — singleton name + friendly workspace host

**Files:**
- Modify: `internal/cli/browser.go` (add two functions next to `browserContainerName`, browser.go:46-48)
- Create: `internal/cli/browser_test.go`

**Interfaces:**
- Produces: `browserSingletonName(project string) string`; `workspaceFriendlyHost(name, project string) string`.

- [ ] **Step 1: Write the failing test**

`internal/cli/browser_test.go`:
```go
package cli

import "testing"

func TestBrowserSingletonName(t *testing.T) {
	if got := browserSingletonName("resume-redux"); got != "cspace-resume-redux-browser" {
		t.Errorf("got %q, want cspace-resume-redux-browser", got)
	}
}

func TestWorkspaceFriendlyHost(t *testing.T) {
	cases := map[string][2]string{
		"mercury.resume-redux.cspace.test": {"mercury", "resume-redux"},
		"venus.demo.cspace.test":           {"Venus", "Demo"}, // lowercased
	}
	for want, in := range cases {
		if got := workspaceFriendlyHost(in[0], in[1]); got != want {
			t.Errorf("workspaceFriendlyHost(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/cli/ -run 'BrowserSingletonName|WorkspaceFriendlyHost' 2>&1 | tail`
Expected: FAIL — `undefined: browserSingletonName` / `undefined: workspaceFriendlyHost`.

- [ ] **Step 3: Add the helpers**

In `internal/cli/browser.go`, immediately after `browserContainerName` (ends at :48), add (and ensure `"strings"` is imported):
```go
// browserSingletonName is the per-PROJECT shared browser sidecar container
// name (Phase 2). One per project, shared by all that project's sandboxes.
func browserSingletonName(project string) string {
	return fmt.Sprintf("cspace-%s-browser", project)
}

// workspaceFriendlyHost is the per-instance hostname the shared browser uses
// to reach a sandbox's workspace: <sandbox>.<project>.cspace.test, resolved by
// the cspace DNS daemon to the instance's vmnet IP. Both labels are lowercased
// to match the daemon's lowercased, case-sensitive registry comparison.
func workspaceFriendlyHost(name, project string) string {
	return strings.ToLower(name) + "." + strings.ToLower(project) + ".cspace.test"
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/cli/ -run 'BrowserSingletonName|WorkspaceFriendlyHost' 2>&1 | tail`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/browser.go internal/cli/browser_test.go
git commit -m "feat(browser): add singleton-name + friendly-workspace-host helpers"
```

---

### Task 2: `registry.CountForProject`

**Files:**
- Modify: `internal/registry/registry.go` (add a method near `List`)
- Modify: `internal/registry/registry_test.go`

**Interfaces:**
- Consumes: existing `Register(Entry)`, `List()`, `Entry{Project, Name}`.
- Produces: `func (r *Registry) CountForProject(project string) (int, error)`.

- [ ] **Step 1: Write the failing test**

Append to `internal/registry/registry_test.go`:
```go
func TestCountForProject(t *testing.T) {
	r := &Registry{Path: filepath.Join(t.TempDir(), "reg.json")}
	for _, e := range []Entry{
		{Project: "alpha", Name: "mercury", IP: "10.0.0.1"},
		{Project: "alpha", Name: "venus", IP: "10.0.0.2"},
		{Project: "beta", Name: "mercury", IP: "10.0.0.3"},
	} {
		if err := r.Register(e); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	for proj, want := range map[string]int{"alpha": 2, "beta": 1, "gamma": 0} {
		got, err := r.CountForProject(proj)
		if err != nil {
			t.Fatalf("count %s: %v", proj, err)
		}
		if got != want {
			t.Errorf("CountForProject(%q) = %d, want %d", proj, got, want)
		}
	}
}
```
(If `filepath` / `Entry` field names differ from this, match the existing `registry_test.go` style — confirm `Entry` has `Project`, `Name`, `IP` and that `Register` keys on project+name.)

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/registry/ -run TestCountForProject 2>&1 | tail`
Expected: FAIL — `r.CountForProject undefined`.

- [ ] **Step 3: Add the method**

In `internal/registry/registry.go`, after `List()` (the method ends ~:168+), add:
```go
// CountForProject returns how many registered sandboxes belong to project.
// Counts all states (a "starting" sibling still needs the shared browser).
// Snapshot semantics: built on List(), so it can race a concurrent
// Register/Unregister — callers that need a teardown decision should
// Unregister first, then count.
func (r *Registry) CountForProject(project string) (int, error) {
	entries, err := r.List()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.Project == project {
			n++
		}
	}
	return n, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/registry/ -run TestCountForProject 2>&1 | tail`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/registry/registry.go internal/registry/registry_test.go
git commit -m "feat(registry): add CountForProject for shared-browser ref-counting"
```

---

### Task 3: `BrowserConfig` + `--no-shared-browser` + `resolveSharedBrowser`

**Files:**
- Modify: `internal/config/config.go` (add `BrowserConfig` type + `Config.Browser` field)
- Modify: `internal/cli/cmd_up.go` (declare `noSharedBrowser`, register the flag, add `resolveSharedBrowser`)
- Modify: `internal/cli/cmd_up_test.go` (table test for `resolveSharedBrowser`)
- Modify: `internal/config/config_test.go` (config-load test for the browser block)

**Interfaces:**
- Produces: `config.BrowserConfig{ Shared *bool }`; `Config.Browser config.BrowserConfig`; `resolveSharedBrowser(cfgBrowser config.BrowserConfig, noSharedBrowser bool) bool`.

- [ ] **Step 1: Write the failing tests**

In `internal/cli/cmd_up_test.go` add:
```go
func TestResolveSharedBrowser(t *testing.T) {
	b := func(v bool) *bool { return &v }
	cases := []struct {
		name      string
		cfgShared *bool
		noShared  bool
		want      bool
	}{
		{"default shared", nil, false, true},
		{"config true", b(true), false, true},
		{"config false", b(false), false, false},
		{"flag off default", nil, true, false},
		{"flag overrides config true", b(true), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveSharedBrowser(config.BrowserConfig{Shared: tc.cfgShared}, tc.noShared)
			if got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
```
(Ensure `cmd_up_test.go` imports `"github.com/elliottregan/cspace/internal/config"`.)

In `internal/config/config_test.go` add a test that loads a project whose `.cspace.json` sets `{"browser":{"shared":false}}` and asserts `cfg.Browser.Shared != nil && *cfg.Browser.Shared == false`. Mirror the existing load-test helper in that file (use the same temp-dir + `Load` pattern already present); the assertion is:
```go
	if cfg.Browser.Shared == nil || *cfg.Browser.Shared != false {
		t.Fatalf("browser.shared: got %v, want explicit false", cfg.Browser.Shared)
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/cli/ -run TestResolveSharedBrowser ./internal/config/ -run 'Browser' 2>&1 | tail`
Expected: FAIL — `resolveSharedBrowser` undefined and `cfg.Browser` undefined.

- [ ] **Step 3: Add the config type + field**

In `internal/config/config.go`, add the `Browser` field to the `Config` struct (after the `Resources` field, ~config.go:39):
```go
	Browser BrowserConfig `json:"browser,omitempty"`
```
And add the type (near `PluginsConfig`, ~config.go:108):
```go
// BrowserConfig controls the cspace browser sidecar topology. Shared is a
// tristate: nil = unset (defaults to shared=true); a pointer lets the
// --no-shared-browser flag distinguish "config said true" from "config left it
// unset" when overriding.
type BrowserConfig struct {
	Shared *bool `json:"shared,omitempty"`
}
```
(No `Load()` change: config blocks are populated by the JSON round-trip of the deep-merged map, config.go:149-158.)

- [ ] **Step 4: Add the flag + resolver to cmd_up.go**

Declare the var next to `noBrowser` (~cmd_up.go:39):
```go
	var noSharedBrowser bool
```
Register the flag next to `--no-browser` (~cmd_up.go:830):
```go
	cmd.Flags().BoolVar(&noSharedBrowser, "no-shared-browser", false,
		"use a per-sandbox browser sidecar instead of the shared project browser (default: shared)")
```
Add the resolver near `resolveBrowserEnabled` (~cmd_up.go:1191):
```go
// resolveSharedBrowser decides whether a project's sandboxes share one
// browser sidecar. Default true; .cspace.json browser.shared overrides the
// default; --no-shared-browser overrides config (applied last = flag wins).
func resolveSharedBrowser(cfgBrowser config.BrowserConfig, noSharedBrowser bool) bool {
	shared := true
	if cfgBrowser.Shared != nil {
		shared = *cfgBrowser.Shared
	}
	if noSharedBrowser {
		shared = false
	}
	return shared
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/cli/ -run TestResolveSharedBrowser ./internal/config/ -run 'Browser' 2>&1 | tail`
Expected: PASS. Then `go build ./... && go vet ./...` → clean.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/cli/cmd_up.go internal/cli/cmd_up_test.go
git commit -m "feat(config): add browser.shared + --no-shared-browser opt-out (flag overrides config)"
```

---

### Task 4: `runBrowserSidecar` extraction + `ensureSharedBrowserSidecar`

**Files:**
- Modify: `internal/cli/browser.go` (refactor `startBrowserSidecar`; add `runBrowserSidecar`, `ensureSharedBrowserSidecar`, a `containerExists` probe)

**Interfaces:**
- Consumes: `browserSingletonName` (Task 1), `browserImage`, `waitForBrowserIP`, `waitForCDP`, `stopBrowserSidecar`, `browserCDPPort`, `browserRunServerPort`, `BrowserSidecar`.
- Produces: `runBrowserSidecar(ctx, containerName, plVersion string) (*BrowserSidecar, error)`; `ensureSharedBrowserSidecar(ctx context.Context, project, plVersion string) (*BrowserSidecar, bool, error)` (the bool is `startedNew`); `startBrowserSidecar` keeps its existing `(ctx, project, sandbox, plVersion) (*BrowserSidecar, error)` signature (per-instance path, unchanged for callers).

- [ ] **Step 1: Verify `container run --label` is supported on this substrate**

Run:
```bash
container run -d --name cspace-labeltest --label cspace.test=1 docker.io/library/alpine:latest sleep 60 >/dev/null 2>&1
container inspect cspace-labeltest 2>/dev/null | grep -i 'cspace.test' && echo "LABEL OK" || echo "LABEL NOT IN INSPECT"
container stop cspace-labeltest >/dev/null 2>&1; container rm cspace-labeltest >/dev/null 2>&1
```
If `LABEL OK`: use `--label` as below. If `LABEL NOT IN INSPECT`: replace the `--label cspace.playwright-version=<v>` arg with an env var `-e CSPACE_PL_VERSION=<v>` and read it from the inspect's env in Step 4's version check. Record which path you took in the report.

- [ ] **Step 2: Extract a non-torching `runBrowserSidecar` and add the `--label`**

In `internal/cli/browser.go`, refactor `startBrowserSidecar` (current :77-181). Move the body from "build args → exec → waitForBrowserIP → build URLs → waitForCDP → return struct" into a new private function that does NOT torch, and add the version label. Insert `runBrowserSidecar` and rewrite `startBrowserSidecar` to torch-then-run:
```go
// runBrowserSidecar starts a sidecar container with the given name. It does
// NOT remove a pre-existing same-named container (callers decide that). The
// cspace.playwright-version label lets the shared path detect a version-mismatched
// singleton on reuse.
func runBrowserSidecar(ctx context.Context, containerName, plVersion string) (*BrowserSidecar, error) {
	if plVersion == "" {
		plVersion = defaultPlaywrightVersion
	}
	args := []string{
		"run", "-d",
		"--name", containerName,
		"--label", "cspace.playwright-version=" + plVersion,
		"--dns", "1.1.1.1",
		"--dns", "8.8.8.8",
		browserImage(plVersion),
		"bash", "-c",
		// <<< KEEP the existing bash command string byte-for-byte (browser.go:96-149) >>>
	}
	cmd := exec.CommandContext(ctx, "container", args...)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return nil, fmt.Errorf("start browser sidecar: %w (%s)", runErr, string(out))
	}
	ip, err := waitForBrowserIP(ctx, containerName, 30*time.Second)
	if err != nil {
		stopBrowserSidecar(context.Background(), containerName)
		return nil, err
	}
	cdpURL := fmt.Sprintf("http://%s:%d", ip, browserCDPPort)
	wsURL := fmt.Sprintf("ws://%s:%d/", ip, browserRunServerPort)
	if err := waitForCDP(ctx, cdpURL, 90*time.Second); err != nil {
		stopBrowserSidecar(context.Background(), containerName)
		return nil, err
	}
	return &BrowserSidecar{ContainerName: containerName, IP: ip, CDPURL: cdpURL, RunServerWSURL: wsURL}, nil
}

// startBrowserSidecar is the per-instance path (opt-out / --no-shared-browser):
// it torches any prior same-named container then runs fresh. Unchanged signature.
func startBrowserSidecar(ctx context.Context, project, sandbox, plVersion string) (*BrowserSidecar, error) {
	containerName := browserContainerName(project, sandbox)
	// Idempotency: torch any prior container with the same name.
	_ = exec.CommandContext(ctx, "container", "stop", containerName).Run()
	_ = exec.CommandContext(ctx, "container", "rm", containerName).Run()
	return runBrowserSidecar(ctx, containerName, plVersion)
}
```
**Copy the existing bash command string verbatim** from the current `startBrowserSidecar` (browser.go ~:96-149) into the `runBrowserSidecar` args where marked — do not retype it. If Step 1 said `LABEL NOT IN INSPECT`, replace `"--label", "cspace.playwright-version=" + plVersion,` with `"-e", "CSPACE_PL_VERSION=" + plVersion,`.

- [ ] **Step 3: Add a `containerExists` probe**

Add near `stopBrowserSidecar`:
```go
// containerExists reports whether a container with this name exists (any state).
func containerExists(ctx context.Context, name string) bool {
	return exec.CommandContext(ctx, "container", "inspect", name).Run() == nil
}
```

- [ ] **Step 4: Add `ensureSharedBrowserSidecar`**

Add (returns `startedNew` so the caller's error-teardown only stops a sidecar this `up` created):
```go
// ensureSharedBrowserSidecar returns the project's shared browser sidecar,
// starting it if absent and REUSING it if a healthy, version-matched one is
// already running. Never torches a healthy singleton. The bool is startedNew:
// true iff this call created the container (the caller uses it to gate
// error-path teardown so a reused singleton is never stopped).
func ensureSharedBrowserSidecar(ctx context.Context, project, plVersion string) (*BrowserSidecar, bool, error) {
	if plVersion == "" {
		plVersion = defaultPlaywrightVersion
	}
	name := browserSingletonName(project)

	if containerExists(ctx, name) {
		// Healthy + version-matched? Reuse without torching.
		ip, ipErr := waitForBrowserIP(ctx, name, 5*time.Second)
		if ipErr == nil {
			cdpURL := fmt.Sprintf("http://%s:%d", ip, browserCDPPort)
			if waitForCDP(ctx, cdpURL, 10*time.Second) == nil && sidecarVersion(ctx, name) == plVersion {
				wsURL := fmt.Sprintf("ws://%s:%d/", ip, browserRunServerPort)
				return &BrowserSidecar{ContainerName: name, IP: ip, CDPURL: cdpURL, RunServerWSURL: wsURL}, false, nil
			}
		}
		// Exists but unhealthy / version-mismatched: torch then restart.
		stopBrowserSidecar(ctx, name)
	}
	bs, err := runBrowserSidecar(ctx, name, plVersion)
	if err != nil {
		// Concurrency: a sibling `up` may have created it between our check and
		// run ("already exists"). Re-probe and reuse if it's now healthy.
		if containerExists(ctx, name) {
			if ip, ipErr := waitForBrowserIP(ctx, name, 5*time.Second); ipErr == nil {
				cdpURL := fmt.Sprintf("http://%s:%d", ip, browserCDPPort)
				if waitForCDP(ctx, cdpURL, 10*time.Second) == nil {
					wsURL := fmt.Sprintf("ws://%s:%d/", ip, browserRunServerPort)
					return &BrowserSidecar{ContainerName: name, IP: ip, CDPURL: cdpURL, RunServerWSURL: wsURL}, false, nil
				}
			}
		}
		return nil, false, err
	}
	return bs, true, nil
}

// sidecarVersion reads the cspace.playwright-version recorded on the running
// sidecar (the --label from runBrowserSidecar). Returns "" if it can't be read,
// which forces a conservative restart on reuse. Adjust the grep to the actual
// `container inspect` JSON shape confirmed in Step 1 (label vs env path).
func sidecarVersion(ctx context.Context, name string) string {
	out, err := exec.CommandContext(ctx, "container", "inspect", name).Output()
	if err != nil {
		return ""
	}
	// cspace.playwright-version appears once, as "<key>":"<value>" or
	// "key=value" in the labels/env block. Extract the value after the marker.
	s := string(out)
	const marker = "cspace.playwright-version"
	i := strings.Index(s, marker)
	if i < 0 {
		return ""
	}
	rest := s[i+len(marker):]
	// skip non-version chars (": =\") then read until the next quote/space/comma
	rest = strings.TrimLeft(rest, "\":= ")
	end := strings.IndexAny(rest, "\", \n}")
	if end < 0 {
		return ""
	}
	return rest[:end]
}
```
If Step 1 chose the `-e` env path, the `sidecarVersion` marker (`cspace.playwright-version`) won't be present — change the marker to `CSPACE_PL_VERSION` (the env key) instead.

- [ ] **Step 5: Build + vet**

Run: `go build ./... && go vet ./... && echo OK`
Expected: `OK`. (Behavior is exercised by the Task 7 integration test — there is no unit test here because these exec the `container` CLI.)

- [ ] **Step 6: Commit**

```bash
git add internal/cli/browser.go
git commit -m "feat(browser): add ensureSharedBrowserSidecar (reuse-if-healthy singleton) + non-torching runBrowserSidecar"
```

---

### Task 5: Wire `cspace up` to the shared sidecar + friendly DNS

**Files:**
- Modify: `internal/cli/cmd_up.go` (the `if browserEnabled {` block ~:526-564, the workspace-host line ~:556, the host-injection blocks ~:662-671 and ~:700-707)

**Interfaces:**
- Consumes: `ensureSharedBrowserSidecar`, `startBrowserSidecar`, `resolveSharedBrowser`, `workspaceFriendlyHost`.

- [ ] **Step 1: Choose shared vs per-instance + capture `startedNew`**

Inside `if browserEnabled {` (before the sidecar start, ~:528) add:
```go
				shared := resolveSharedBrowser(cfg.Browser, noSharedBrowser)
```
Replace the start call (cmd_up.go:536-539):
```go
					bs, berr := startBrowserSidecar(ctx, project, name, plVersion)
					if berr != nil {
						return fmt.Errorf("browser sidecar: %w", berr)
					}
```
with:
```go
					var bs *BrowserSidecar
					var startedNew bool
					var berr error
					if shared {
						bs, startedNew, berr = ensureSharedBrowserSidecar(ctx, project, plVersion)
					} else {
						bs, berr = startBrowserSidecar(ctx, project, name, plVersion)
						startedNew = true
					}
					if berr != nil {
						return fmt.Errorf("browser sidecar: %w", berr)
					}
```
(`cfg` is null-checked elsewhere in this RunE — if `cfg` can be nil here, use `config.BrowserConfig{}`: `var b config.BrowserConfig; if cfg != nil { b = cfg.Browser }; shared := resolveSharedBrowser(b, noSharedBrowser)`.)

- [ ] **Step 2: Gate the error-teardown defer on `startedNew`**

Replace the deferred teardown (cmd_up.go:560-564):
```go
					defer func() {
						if err != nil {
							stopBrowserSidecar(context.Background(), bs.ContainerName)
						}
					}()
```
with:
```go
					defer func() {
						// Only tear down a sidecar THIS up created. A reused
						// shared singleton is left for the instances still using it.
						if err != nil && startedNew {
							stopBrowserSidecar(context.Background(), bs.ContainerName)
						}
					}()
```

- [ ] **Step 3: Friendly per-instance workspace host**

Replace cmd_up.go:556:
```go
					env["CSPACE_WORKSPACE_HOST"] = "workspace"
```
with:
```go
					env["CSPACE_WORKSPACE_HOST"] = workspaceFriendlyHost(name, project)
```

- [ ] **Step 4: Drop the browser-sidecar `/etc/hosts` injection**

Replace the workspace-host injection block (cmd_up.go:662-671):
```go
				if browserSidecar != nil {
					if hErr := InjectWorkspaceHost(ctx, browserSidecar.ContainerName, ip); hErr != nil {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"[cspace] warning: inject workspace host into browser sidecar: %v\n", hErr)
					}
					if hErr := InjectWorkspaceHost(ctx, containerName, "127.0.0.1"); hErr != nil {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"[cspace] warning: inject workspace host into workspace: %v\n", hErr)
					}
				}
```
with (keep ONLY the workspace's own loopback entry; the shared browser resolves the friendly name via the daemon):
```go
				if browserSidecar != nil {
					if hErr := InjectWorkspaceHost(ctx, containerName, "127.0.0.1"); hErr != nil {
						_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
							"[cspace] warning: inject workspace host into workspace: %v\n", hErr)
					}
				}
```
And delete the compose-sidecar injection block entirely (cmd_up.go:700-707):
```go
					if browserSidecar != nil {
						hosts := orch.ServiceIPs()
						hosts["workspace"] = ip
						if hErr := InjectHosts(ctx, browserSidecar.ContainerName, hosts); hErr != nil {
							_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
								"[cspace] warning: extend browser sidecar hosts: %v\n", hErr)
						}
					}
```
(If removing that block leaves `orch.ServiceIPs()` unused or an empty `if`, clean up so it compiles. The compose-sidecar microVMs still get their own hostnames from the orchestrator's own injection — only the *browser sidecar*'s copy is dropped.)

- [ ] **Step 5: Build + vet**

Run: `go build ./... && go vet ./... && echo OK`
Expected: `OK` (watch for an unused `ip` or `orch` variable after the deletions — if `ip` is now only used for the kept loopback line and the registry, it stays; if a variable goes unused, address it).

- [ ] **Step 6: Commit**

```bash
git add internal/cli/cmd_up.go
git commit -m "feat(up): use the shared project browser + friendly-DNS workspace host; drop browser /etc/hosts injection"
```

---

### Task 6: Ref-counted teardown in `cspace down`

**Files:**
- Modify: `internal/cli/cmd_down.go` (`teardownSandbox`, the tail at :172, :214-225)

**Interfaces:**
- Consumes: `browserSingletonName`, `browserContainerName`, `stopBrowserSidecar`, `r.Unregister`, `r.List`.

- [ ] **Step 1: Remove the per-entry browser stop and the now-unused `entry` lookup**

In `teardownSandbox`, delete the `entry, _ := r.Lookup(project, name)` line (cmd_down.go:172) and the browser stop block (cmd_down.go:217-220):
```go
	// CDP connections drain naturally. stopBrowserSidecar is idempotent.
	if entry.BrowserContainer != "" {
		stopBrowserSidecar(ctx, entry.BrowserContainer)
	}
```
(Removing the `entry.BrowserContainer` use is why the `entry` lookup must also go — otherwise `entry` is an unused variable and the build fails.)

- [ ] **Step 2: Add per-instance teardown + ref-counted shared teardown**

Replace the region from `_ = a.Stop(...)` (cmd_down.go:214) through `_ = r.Unregister(project, name)` (cmd_down.go:222) with:
```go
	_ = a.Stop(ctx, fmt.Sprintf("cspace-%s-%s", project, name))

	// Per-instance (opt-out / --no-shared-browser) sidecar: stop this sandbox's
	// own browser. Idempotent and a no-op in the shared case (no such container).
	stopBrowserSidecar(ctx, browserContainerName(project, name))

	// Remove this instance from the registry BEFORE counting so it is not
	// included in the remaining-sandboxes tally.
	_ = r.Unregister(project, name)

	// Shared browser sidecar: ref-counted — stop it only when this was the last
	// sandbox in the project. Idempotent and a no-op when no singleton exists.
	if remaining, err := r.CountForProject(project); err != nil {
		_, _ = fmt.Fprintf(out, "[cspace] warning: registry count during browser teardown: %v\n", err)
	} else if remaining == 0 {
		stopBrowserSidecar(ctx, browserSingletonName(project))
	}
```

- [ ] **Step 3: Build + vet**

Run: `go build ./... && go vet ./... && echo OK`
Expected: `OK` (no unused `entry`; `CountForProject` from Task 2 is in scope).

- [ ] **Step 4: Run the Go suite (no env-gated tests)**

Run: `go test ./... -skip 'TestCspaceLifecycle' 2>&1 | grep -E '^(FAIL|ok)' | grep FAIL || echo "all packages PASS"`
Expected: `all packages PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cmd_down.go
git commit -m "feat(down): ref-counted teardown of the shared project browser sidecar"
```

---

### Task 7: Integration acceptance (environment-gated)

**Files:** none modified (acceptance verification). Requires Apple Container + a valid Anthropic credential for the resume-redux drive.

- [ ] **Step 1: Build the image and boot two instances of a compose-less project**

Run (from a small test project, or cspace itself):
```bash
make cspace-image
./bin/cspace-go up alpha
./bin/cspace-go up beta
```
Expected: both boot; the browser sidecar is shared.

- [ ] **Step 2: Assert one shared sidecar serves both**

Run:
```bash
container ls --format '{{.Names}}' 2>/dev/null | grep -E 'browser$'
```
Expected: exactly one `cspace-<project>-browser` (NOT `cspace-<project>-alpha-browser` / `-beta-browser`). Confirm both instances' `CSPACE_BROWSER_CDP_URL` point at it:
```bash
./bin/cspace-go ssh alpha -- sh -lc 'echo $CSPACE_BROWSER_CDP_URL; echo $CSPACE_WORKSPACE_HOST'
./bin/cspace-go ssh beta  -- sh -lc 'echo $CSPACE_BROWSER_CDP_URL; echo $CSPACE_WORKSPACE_HOST'
```
Expected: same CDP URL for both; `CSPACE_WORKSPACE_HOST` = `alpha.<project>.cspace.test` / `beta.<project>.cspace.test` respectively.

- [ ] **Step 3: Assert friendly-DNS resolves per-instance inside the shared browser**

Run:
```bash
container exec cspace-<project>-browser sh -lc 'getent hosts alpha.<project>.cspace.test; getent hosts beta.<project>.cspace.test'
```
Expected: two DIFFERENT IPs (each instance's workspace IP) — proving per-instance resolution with no `/etc/hosts` collision.

- [ ] **Step 4: Assert Playwright context isolation on the shared browser**

Drive each instance's `cspace-playwright` to set a cookie on a shared origin and confirm the sibling does not see it (reuse the Phase 1 isolation check pattern). Expected: each instance sees only its own cookie.

- [ ] **Step 5: Assert ref-counted teardown**

Run:
```bash
./bin/cspace-go down alpha
container ls --format '{{.Names}}' | grep 'cspace-<project>-browser' && echo "shared sidecar STILL UP (correct)"
./bin/cspace-go down beta
container ls --format '{{.Names}}' | grep 'cspace-<project>-browser' && echo "LEAK (bad)" || echo "shared sidecar STOPPED on last down (correct)"
```
Expected: up after `alpha` down; stopped after `beta` down.

- [ ] **Step 6: Assert the `--no-shared-browser` opt-out**

Run:
```bash
./bin/cspace-go up gamma --no-shared-browser
container ls --format '{{.Names}}' | grep 'cspace-<project>-gamma-browser' && echo "per-instance sidecar (correct)"
./bin/cspace-go down gamma
```
Expected: a per-instance `cspace-<project>-gamma-browser`; gone after down.

- [ ] **Step 7: resume-redux acceptance (the bar)**

From `~/Projects/resume-redux`, boot two instances on the shared browser, run the e2e suite per-instance, and drive the MCP agent to load the site per-instance (Convex via the workspace proxy). Confirm e2e passes for both, the agent reaches each instance's site, and ref-counted teardown leaves no leak. (resume-redux needs no change — `scripts/e2e.sh` reads `CSPACE_WORKSPACE_HOST`; browser→Convex rides the `__SELF__` workspace proxy.)

- [ ] **Step 8: Tear down + final marker commit**

```bash
./bin/cspace-go down alpha 2>/dev/null; ./bin/cspace-go down beta 2>/dev/null
git commit --allow-empty -m "test: verify shared browser — one sidecar, per-instance friendly DNS, context isolation, ref-counted teardown, opt-out, resume-redux e2e+MCP"
```

---

### Task 8: Make the daemon's gateway DNS listener retry the bind (acceptance-driven)

**Files:**
- Modify: `internal/cli/cmd_daemon.go` (the two gateway-listener goroutines, ~lines 218-231)

**Why:** The Task 7 acceptance run found that in-container `*.cspace.test` resolution (which the shared browser now depends on, since Phase 2 dropped bare-name `/etc/hosts` injection) was NXDOMAIN. Root cause: the daemon's gateway DNS listener binds the specific gateway IP `192.168.64.1:5354`, but that IP lives on the vmnet bridge (`bridge101`) which doesn't exist until the first container boots — and `cspace up` spawns the daemon BEFORE the container. The current bind is one-shot best-effort, so it fails permanently. A freshly-spawned daemon (bridge already up) binds it fine and the sidecar resolves correctly — so the fix is to RETRY the gateway bind until the bridge appears. (This overturns the spec's "no daemon change" note; the host-loopback listener is unaffected and stays fatal-on-failure.)

**Interfaces:** none new — behavior change only.

- [ ] **Step 1: Replace the two one-shot gateway-listener goroutines with retrying loops**

In `internal/cli/cmd_daemon.go`, replace the gateway-bind block (currently ~lines 214-231):
```go
	// Gateway bind — best-effort. Containers query 192.168.64.1:5354
	// for cspace.test lookups (via dnsmasq forwarder inside the
	// sandbox). Failure here just means in-container hostname
	// resolution stops working; the host path is unaffected.
	go func() {
		server := &dns.Server{Addr: daemonDNSGatewayAddr, Net: "udp", Handler: dh}
		log.Printf("cspace daemon: DNS listening on %s/udp (containers)", daemonDNSGatewayAddr)
		if err := server.ListenAndServe(); err != nil {
			log.Printf("WARN: cspace daemon DNS UDP bind on %s failed: %v", daemonDNSGatewayAddr, err)
			log.Printf("      in-container *.cspace.test lookups will NXDOMAIN until this is resolved.")
		}
	}()
	go func() {
		server := &dns.Server{Addr: daemonDNSGatewayAddr, Net: "tcp", Handler: dh}
		if err := server.ListenAndServe(); err != nil {
			log.Printf("WARN: cspace daemon DNS TCP bind on %s failed: %v", daemonDNSGatewayAddr, err)
		}
	}()
```
with retrying loops (a successful `ListenAndServe` blocks while serving; a bind failure returns immediately and we retry after a short delay so the listener comes up shortly after the first sandbox boots):
```go
	// Gateway bind — best-effort WITH RETRY. The vmnet bridge that owns
	// 192.168.64.1 doesn't exist until the first container boots, and the
	// daemon is spawned by `cspace up` BEFORE that — so the initial bind
	// loses a startup race. Retry until it binds so in-container
	// *.cspace.test resolution (which the shared browser sidecar depends on)
	// comes up shortly after the first sandbox starts. The host-loopback
	// listener above is unaffected.
	go func() {
		for {
			server := &dns.Server{Addr: daemonDNSGatewayAddr, Net: "udp", Handler: dh}
			log.Printf("cspace daemon: DNS listening on %s/udp (containers)", daemonDNSGatewayAddr)
			err := server.ListenAndServe()
			log.Printf("WARN: cspace daemon DNS UDP bind on %s failed: %v; retrying in 3s "+
				"(in-container *.cspace.test lookups NXDOMAIN until this binds)", daemonDNSGatewayAddr, err)
			time.Sleep(3 * time.Second)
		}
	}()
	go func() {
		for {
			server := &dns.Server{Addr: daemonDNSGatewayAddr, Net: "tcp", Handler: dh}
			if err := server.ListenAndServe(); err != nil {
				log.Printf("WARN: cspace daemon DNS TCP bind on %s failed: %v; retrying in 3s", daemonDNSGatewayAddr, err)
			}
			time.Sleep(3 * time.Second)
		}
	}()
```
Ensure `"time"` is imported (it almost certainly already is; add it if not).

- [ ] **Step 2: Build + vet**

Run: `go build ./... && go vet ./... && echo OK`
Expected: `OK`. (No unit test — this is a network-bind retry loop; verified by the integration re-check below.)

- [ ] **Step 3: Integration re-verify (the original failing case)**

```bash
make build
pkill -f 'cspace daemon serve' 2>/dev/null; sleep 1   # clear any stale daemon
./bin/cspace-go up alpha                                # spawns daemon (no bridge yet), then boots the container
sleep 6                                                 # let the retry catch the now-up bridge
lsof -nP -iUDP:5354 | grep -q '192.168.64.1:5354' && echo "gateway listener BOUND (correct)" || echo "gateway STILL DOWN (bug)"
container exec cspace-cspace-browser getent hosts alpha.cspace.cspace.test && echo "sidecar RESOLVES friendly name (correct)"
./bin/cspace-go down alpha
```
Expected: `gateway listener BOUND`, and the sidecar resolves `alpha.cspace.cspace.test` to alpha's IP.

- [ ] **Step 4: Commit**

```bash
git add internal/cli/cmd_daemon.go
git commit -m "fix(daemon): retry the gateway DNS bind so in-container .cspace.test resolves (shared browser depends on it)"
```
