// Package scripts contains Go-driven tests for bash scripts shipped in lib/scripts.
// We keep these tests in Go so they run as part of `make test` without needing
// an extra bats/shellcheck toolchain.
package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeScript copies the named script from repo_root/lib/scripts into tmp
// and returns the copy path. The copy is needed because we inject a stubbed
// PATH around it.
func writeScript(t *testing.T, repoRoot, name, tmp string) string {
	t.Helper()
	src := filepath.Join(repoRoot, "lib", "scripts", name)
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("reading %s: %v", src, err)
	}
	dst := filepath.Join(tmp, name)
	if err := os.WriteFile(dst, data, 0755); err != nil {
		t.Fatalf("writing %s: %v", dst, err)
	}
	return dst
}

// repoRoot returns the path to the cspace repo root for loading lib/scripts.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestTeleportPrepareHappyPath(t *testing.T) {
	tmp := t.TempDir()

	// Fake HOME with an active Claude session transcript.
	home := filepath.Join(tmp, "home")
	projects := filepath.Join(home, ".claude", "projects", "-workspace")
	if err := os.MkdirAll(projects, 0755); err != nil {
		t.Fatal(err)
	}
	sessionID := "abc123"
	transcript := filepath.Join(projects, sessionID+".jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"user"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Fake /workspace with a minimal git repo (so `git bundle create --all` works).
	workspace := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "init"},
	} {
		c := exec.Command(cmd[0], cmd[1:]...)
		c.Dir = workspace
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", cmd, out)
		}
	}

	// Fake /teleport transfer dir.
	teleport := filepath.Join(tmp, "teleport")
	if err := os.MkdirAll(teleport, 0755); err != nil {
		t.Fatal(err)
	}

	// Stub cspace on PATH: records its args to a file.
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	cspaceStub := filepath.Join(binDir, "cspace")
	argsLog := filepath.Join(tmp, "cspace.args")
	stubSrc := "#!/usr/bin/env bash\nprintf '%s\\n' \"$@\" >> " + argsLog + "\nexit 0\n"
	if err := os.WriteFile(cspaceStub, []byte(stubSrc), 0755); err != nil {
		t.Fatal(err)
	}

	script := writeScript(t, repoRoot(t), "teleport-prepare.sh", tmp)

	cmd := exec.Command("bash", script, "venus")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"CSPACE_TELEPORT_WORKSPACE="+workspace,
		"CSPACE_TELEPORT_DIR="+teleport,
		"CSPACE_INSTANCE_NAME=mercury",
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script failed: %v\n%s", err, out)
	}

	sessionDir := filepath.Join(teleport, sessionID)
	// session.jsonl is no longer staged — it travels via the shared
	// $HOME/.cspace/sessions/<project>/ bind mount instead, so the
	// teleport bundle only carries workspace state + manifest.
	for _, f := range []string{"workspace.bundle", "manifest.json"} {
		p := filepath.Join(sessionDir, f)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s: %v", p, err)
		}
	}
	// Assert that session.jsonl is NOT staged (regression: catches future
	// reintroduction of the copy that the shared-sessions mount obsoletes).
	if _, err := os.Stat(filepath.Join(sessionDir, "session.jsonl")); err == nil {
		t.Error("session.jsonl should not be in the teleport bundle anymore")
	}

	// Verify bundle is valid.
	verify := exec.Command("git", "bundle", "verify", filepath.Join(sessionDir, "workspace.bundle"))
	verify.Dir = workspace
	if out, err := verify.CombinedOutput(); err != nil {
		t.Errorf("bundle verify: %v\n%s", err, out)
	}

	manifestBytes, err := os.ReadFile(filepath.Join(sessionDir, "manifest.json"))
	if err != nil {
		t.Fatalf("reading manifest.json: %v", err)
	}
	if !strings.Contains(string(manifestBytes), "source_remote_url") {
		t.Errorf("manifest.json missing source_remote_url key: %s", string(manifestBytes))
	}

	// Verify cspace stub was invoked with the right shape.
	args, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("reading stub log: %v", err)
	}
	argStr := string(args)
	wantFragments := []string{"up", "venus", "--teleport-from", sessionDir, "stop", "mercury"}
	for _, w := range wantFragments {
		if !strings.Contains(argStr, w) {
			t.Errorf("cspace args missing %q; got:\n%s", w, argStr)
		}
	}
}

func TestTeleportPrepareAbortsWithoutSession(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects", "-workspace"), 0755); err != nil {
		t.Fatal(err)
	}
	// No .jsonl written — no active session.

	script := writeScript(t, repoRoot(t), "teleport-prepare.sh", tmp)
	cmd := exec.Command("bash", script, "venus")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"CSPACE_TELEPORT_WORKSPACE="+tmp,
		"CSPACE_TELEPORT_DIR="+tmp,
		"CSPACE_INSTANCE_NAME=mercury",
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure, got success:\n%s", out)
	}
	if !strings.Contains(string(out), "live Claude session") {
		t.Errorf("expected abort message, got:\n%s", out)
	}
}

func TestTeleportPrepareKeepsSourceWhenUpFails(t *testing.T) {
	tmp := t.TempDir()

	// Fake HOME with an active Claude session transcript.
	home := filepath.Join(tmp, "home")
	projects := filepath.Join(home, ".claude", "projects", "-workspace")
	if err := os.MkdirAll(projects, 0755); err != nil {
		t.Fatal(err)
	}
	sessionID := "abc123"
	transcript := filepath.Join(projects, sessionID+".jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"user"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Fake /workspace with a minimal git repo.
	workspace := filepath.Join(tmp, "workspace")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "init"},
	} {
		c := exec.Command(cmd[0], cmd[1:]...)
		c.Dir = workspace
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", cmd, out)
		}
	}

	teleport := filepath.Join(tmp, "teleport")
	if err := os.MkdirAll(teleport, 0755); err != nil {
		t.Fatal(err)
	}

	// Stub cspace: fails (exit 1) when first positional arg is "up";
	// records all args to the log regardless.
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatal(err)
	}
	argsLog := filepath.Join(tmp, "cspace.args")
	stubSrc := "#!/usr/bin/env bash\n" +
		"printf '%s\\n' \"$@\" >> " + argsLog + "\n" +
		"if [ \"$1\" = \"up\" ]; then exit 1; fi\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "cspace"), []byte(stubSrc), 0755); err != nil {
		t.Fatal(err)
	}

	script := writeScript(t, repoRoot(t), "teleport-prepare.sh", tmp)

	cmd := exec.Command("bash", script, "venus")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"CSPACE_TELEPORT_WORKSPACE="+workspace,
		"CSPACE_TELEPORT_DIR="+teleport,
		"CSPACE_INSTANCE_NAME=mercury",
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when `cspace up` fails, got success:\n%s", out)
	}

	args, readErr := os.ReadFile(argsLog)
	if readErr != nil {
		t.Fatalf("reading stub log: %v", readErr)
	}
	argStr := string(args)
	if !strings.Contains(argStr, "up") {
		t.Errorf("expected `up` in stub args (the up attempt should have happened); got:\n%s", argStr)
	}
	if strings.Contains(argStr, "stop") {
		t.Errorf("expected no `stop` in stub args (source must not be stopped when up fails); got:\n%s", argStr)
	}
}
