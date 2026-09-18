package assets

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestDefaultsJSON_Parses(t *testing.T) {
	data, err := DefaultsJSON()
	if err != nil {
		t.Fatalf("DefaultsJSON() error: %v", err)
	}

	if len(data) == 0 {
		t.Fatal("DefaultsJSON() returned empty data")
	}

	// Verify it's valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("DefaultsJSON() returned invalid JSON: %v", err)
	}

	// Check that expected top-level keys exist (the rc.39 removal cut
	// deleted the dead claude/verify blocks; see config.Config for the
	// consumed surface)
	expectedKeys := []string{"project", "container", "firewall", "agent", "plugins", "services", "mcpServers"}
	for _, key := range expectedKeys {
		if _, ok := parsed[key]; !ok {
			t.Errorf("defaults.json missing expected key: %s", key)
		}
	}
}

func TestDefaultsJSON_FirewallEnabledTrue(t *testing.T) {
	data, err := DefaultsJSON()
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	fw := parsed["firewall"].(map[string]interface{})
	if fw["enabled"] != true {
		t.Errorf("expected firewall.enabled=true, got %v", fw["enabled"])
	}
}

func TestExtractTo_CreatesFiles(t *testing.T) {
	dir := t.TempDir()

	extractRoot, err := ExtractTo(dir, "test-version")
	if err != nil {
		t.Fatalf("ExtractTo() error: %v", err)
	}

	// Check key files exist
	keyFiles := []string{
		"defaults.json",
		"templates/Dockerfile",
		"runtime/scripts/cspace-entrypoint.sh",
		"runtime/scripts/cspace-supervisor-loop.sh",
	}

	for _, f := range keyFiles {
		path := filepath.Join(extractRoot, f)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file to exist: %s", path)
		}
	}
}

func TestExtractTo_PreservesStructure(t *testing.T) {
	dir := t.TempDir()

	extractRoot, err := ExtractTo(dir, "test-version")
	if err != nil {
		t.Fatalf("ExtractTo() error: %v", err)
	}

	// Check directory structure
	expectedDirs := []string{
		"templates",
		"runtime/scripts",
		"agent-supervisor-bun",
	}

	for _, d := range expectedDirs {
		path := filepath.Join(extractRoot, d)
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			t.Errorf("expected directory to exist: %s", path)
			continue
		}
		if !info.IsDir() {
			t.Errorf("expected %s to be a directory", path)
		}
	}
}

func TestExtractTo_ShellScriptsExecutable(t *testing.T) {
	dir := t.TempDir()

	extractRoot, err := ExtractTo(dir, "test-version")
	if err != nil {
		t.Fatalf("ExtractTo() error: %v", err)
	}

	// Check that .sh files are executable
	shFiles := []string{
		"runtime/scripts/cspace-entrypoint.sh",
		"runtime/scripts/cspace-supervisor-loop.sh",
	}

	for _, f := range shFiles {
		path := filepath.Join(extractRoot, f)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("stat %s: %v", path, err)
			continue
		}
		mode := info.Mode()
		if mode&0111 == 0 {
			t.Errorf("expected %s to be executable (mode=%v)", f, mode)
		}
	}
}

func TestExtractTo_VersionMarker(t *testing.T) {
	dir := t.TempDir()

	extractRoot, err := ExtractTo(dir, "v1.0.0")
	if err != nil {
		t.Fatalf("ExtractTo() error: %v", err)
	}

	// Check version marker
	markerPath := filepath.Join(extractRoot, ".version")
	data, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("reading version marker: %v", err)
	}
	if string(data) != "v1.0.0\n" {
		t.Errorf("expected version marker=v1.0.0, got %q", string(data))
	}
}

func TestExtractTo_SkipsIfVersionMatches(t *testing.T) {
	dir := t.TempDir()

	// First extraction
	extractRoot, err := ExtractTo(dir, "v1.0.0")
	if err != nil {
		t.Fatalf("first ExtractTo() error: %v", err)
	}

	// Modify a file to verify it's NOT overwritten on re-extraction
	testFile := filepath.Join(extractRoot, "defaults.json")
	if err := os.WriteFile(testFile, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	// Second extraction with same version — should skip
	_, err = ExtractTo(dir, "v1.0.0")
	if err != nil {
		t.Fatalf("second ExtractTo() error: %v", err)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "modified" {
		t.Error("expected extraction to be skipped (file was overwritten)")
	}
}

func TestExtractTo_ReExtractsOnVersionChange(t *testing.T) {
	dir := t.TempDir()

	// First extraction
	extractRoot, err := ExtractTo(dir, "v1.0.0")
	if err != nil {
		t.Fatalf("first ExtractTo() error: %v", err)
	}

	// Modify a file
	testFile := filepath.Join(extractRoot, "defaults.json")
	if err := os.WriteFile(testFile, []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}

	// Second extraction with different version — should re-extract
	_, err = ExtractTo(dir, "v2.0.0")
	if err != nil {
		t.Fatalf("second ExtractTo() error: %v", err)
	}

	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "modified" {
		t.Error("expected re-extraction to overwrite modified file")
	}
}

// TestEmbeddedRuntimeCarriesTmuxConf — tmux.conf is the first file under
// lib/runtime/ that is not a .sh, so no existing sync-embedded glob sweeps
// it. Without its own cp rule the embedded tree (and therefore any
// `cspace image build` outside a source checkout) silently loses it.
func TestEmbeddedRuntimeCarriesTmuxConf(t *testing.T) {
	runtimeFS, err := RuntimeFS()
	if err != nil {
		t.Fatalf("RuntimeFS() error: %v", err)
	}
	data, err := fs.ReadFile(runtimeFS, "tmux.conf")
	if err != nil {
		t.Fatalf("embedded runtime/tmux.conf missing: %v", err)
	}
	conf := string(data)

	// extended-keys=on silently swallows Shift+Enter in CSI-u form; only
	// `always` passes it through byte-for-byte. Verified on tmux 3.3a.
	if !strings.Contains(conf, "set -g extended-keys always") {
		t.Error("tmux.conf must set `extended-keys always`, not `on`")
	}
	// tmux 3.3a (bookworm) does not know extended-keys-format and rejects
	// the line, which would poison the whole config. Matched as a directive
	// on a non-comment line, not as a substring: the config's own header
	// comment names the option to explain why it is absent, so a substring
	// match would fail on that comment. `[^#\n]*` stops at the first `#`, so
	// neither a full-line comment nor a trailing one can trigger this.
	if regexp.MustCompile(`(?m)^[^#\n]*\bextended-keys-format\b`).MatchString(conf) {
		t.Error("tmux.conf sets extended-keys-format, which tmux 3.3a rejects as invalid")
	}
	// The host owns every key: a prefix would eat one of Claude's.
	if !strings.Contains(conf, "set -g prefix None") {
		t.Error("tmux.conf must unset the prefix so the host owns every key")
	}
	if !strings.Contains(conf, "set -g status off") {
		t.Error("tmux.conf must turn the status bar off")
	}
}

// TestDockerfileCopiesEveryEmbeddedRuntimeFile guards the per-file COPY trap
// (finding 2026-07-16-per-file-dockerfile-copy-and-gitignored-embedded-assets):
// Apple Container's builder drops the contents of a whole-directory COPY, so
// lib/templates/Dockerfile names runtime files one at a time. A file that
// reaches the build context without a COPY line produces no build error —
// just a missing file inside the sandbox at runtime.
func TestDockerfileCopiesEveryEmbeddedRuntimeFile(t *testing.T) {
	df, err := EmbeddedFS.ReadFile("embedded/templates/Dockerfile")
	if err != nil {
		t.Fatalf("embedded Dockerfile missing: %v", err)
	}

	// Collect every COPY source token: `COPY <src...> <dst>`, skipping
	// --from=/--chown= flags and the final destination argument.
	var sources []string
	for _, line := range strings.Split(string(df), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 3 || !strings.EqualFold(fields[0], "COPY") {
			continue
		}
		for _, tok := range fields[1 : len(fields)-1] {
			if strings.HasPrefix(tok, "--") {
				continue
			}
			sources = append(sources, tok)
		}
	}

	runtimeFS, err := RuntimeFS()
	if err != nil {
		t.Fatalf("RuntimeFS() error: %v", err)
	}
	err = fs.WalkDir(runtimeFS, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		want := "lib/runtime/" + p
		for _, src := range sources {
			if src == want {
				return nil
			}
			if ok, _ := path.Match(src, want); ok {
				return nil
			}
		}
		t.Errorf("no COPY line in lib/templates/Dockerfile ships %s — Apple Container's builder does not recurse directory COPYs, so it will be missing from the image", want)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the embedded runtime tree: %v", err)
	}
}

// TestDockerfileInstallsTmux — the config is useless without the binary, and
// the binary is what `cspace attach` probes for before choosing the tmux argv.
func TestDockerfileInstallsTmux(t *testing.T) {
	df, err := EmbeddedFS.ReadFile("embedded/templates/Dockerfile")
	if err != nil {
		t.Fatalf("embedded Dockerfile missing: %v", err)
	}
	if !regexp.MustCompile(`(?m)^\s+tmux\s+\\$`).Match(df) {
		t.Error("Dockerfile's apt-get install block does not list tmux")
	}
}

// TestEveryRuntimeSourceFileIsEmbedded is the symmetric drift guard to
// TestDockerfileCopiesEveryEmbeddedRuntimeFile: that test walks the already-
// embedded tree and catches a file with no Dockerfile COPY, but says nothing
// about a file under lib/runtime/ that the Makefile's sync-embedded step
// never copies into internal/assets/embedded/runtime/ in the first place —
// such a file would be silently absent from both the embedded tree and any
// image built outside a source checkout, with no test noticing. This walks
// the real lib/runtime/ source tree on disk and asserts every non-test file
// in it made it into RuntimeFS().
func TestEveryRuntimeSourceFileIsEmbedded(t *testing.T) {
	root := repoRootForAssetsTest(t)
	runtimeFS, err := RuntimeFS()
	if err != nil {
		t.Fatalf("RuntimeFS() error: %v", err)
	}

	srcDir := filepath.Join(root, "lib", "runtime")
	err = filepath.WalkDir(srcDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		if strings.HasSuffix(p, ".test.sh") {
			return nil
		}
		rel, relErr := filepath.Rel(srcDir, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if _, statErr := fs.Stat(runtimeFS, rel); statErr != nil {
			t.Errorf("lib/runtime/%s is not in the embedded runtime tree — the Makefile's "+
				"sync-embedded step never copies it, so `cspace image build` will not ship it "+
				"either, even though nothing errors", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking lib/runtime: %v", err)
	}
}

// repoRootForAssetsTest walks up from the package directory to find the
// module root (where go.mod lives), so the test can reach lib/runtime/ on
// disk regardless of the working directory `go test` uses.
func repoRootForAssetsTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root (go.mod) from " + dir)
		}
		dir = parent
	}
}
