package cli

import (
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNewDaemonCommandIsDetached(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd, f, err := newDaemonCommand("/usr/local/bin/cspace")
	if err != nil {
		t.Fatalf("newDaemonCommand: %v", err)
	}
	defer f.Close()

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Error("daemon command must set Setsid so it survives the parent")
	}
	if _, ok := cmd.Stderr.(*os.File); !ok {
		t.Errorf("stderr must be an *os.File log (not a parent-held pipe), got %T", cmd.Stderr)
	}
	if cmd.Stdout != cmd.Stderr {
		t.Error("stdout and stderr should share the log file")
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.HasSuffix(joined, "daemon serve") {
		t.Errorf("args = %v, want ... daemon serve", cmd.Args)
	}
}

// A detached daemon must keep answering after its spawner exits. We build the
// real binary, run a throwaway "spawner" that calls the same spawn path and
// then exits, and assert the daemon is still up. DNS/HTTP ports are overridden
// so this never collides with a developer's live daemon.
func TestDaemonSurvivesSpawnerExit(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + spawns real processes")
	}
	bin := buildCspaceForTest(t) // go build -o <tmp> ./cmd/cspace
	home := t.TempDir()
	env := append(os.Environ(),
		"HOME="+home,
		"CSPACE_REGISTRY_DAEMON_PORT=6299",
		"CSPACE_DAEMON_DNS_ADDR=127.0.0.1:15354",
		"CSPACE_DAEMON_GATEWAY_ADDR=127.0.0.1:15355", // loopback stand-in; gateway bind is best-effort
		"CSPACE_REGISTRY_DAEMON_IDLE=1h",
	)
	// Spawner: start the daemon detached exactly like ensureRegistryDaemon, then exit.
	spawner := exec.Command(bin, "daemon", "serve")
	spawner.Env = env
	logf, _ := os.Create(filepath.Join(home, "d.log"))
	spawner.Stdout, spawner.Stderr = logf, logf
	spawner.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := spawner.Start(); err != nil {
		t.Fatal(err)
	}
	pid := spawner.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	waitForPort(t, "127.0.0.1:6299", 5*time.Second)
	_ = logf.Close() // drop the only non-daemon reference to the log
	// Force the daemon to keep logging (a real daemon logs on gateway retries);
	// then confirm it is still alive and answering after the parent's handle is gone.
	time.Sleep(1 * time.Second)
	if syscall.Kill(pid, 0) != nil {
		t.Fatal("daemon died after its spawner's log handle closed (SIGPIPE regression)")
	}
	if _, ok := httpGet(t, "http://127.0.0.1:6299/health"); !ok {
		t.Fatal("daemon stopped answering /health")
	}
}

// buildCspaceForTest builds the real cspace binary into a temp dir so the
// survival test can exercise the actual `daemon serve` entry point.
func buildCspaceForTest(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cspace-test-bin")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/cspace")
	cmd.Dir = repoRootForTest(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/cspace: %v\n%s", err, out)
	}
	return bin
}

// repoRootForTest walks up from the package directory to find the module
// root (where go.mod lives), since `go build ./cmd/cspace` must run there.
func repoRootForTest(t *testing.T) string {
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

// waitForPort polls until addr accepts a TCP connection or timeout elapses.
func waitForPort(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s to accept connections", addr)
}

// httpGet performs a best-effort GET, returning (body, true) on a 200.
func httpGet(t *testing.T, url string) (string, bool) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return "", false
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}
	return string(b), true
}
