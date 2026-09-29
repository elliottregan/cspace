package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// PID-directed shutdown needs no controlling terminal. Keep both signal
// recipients in subprocesses so the test never signals the go test process.
func TestRunAttachChildBoundsIgnoredShutdownSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(sig.String(), func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestRunAttachSignalHelper$", "--", "wrapper", dir)
			cmd.Env = append(os.Environ(), "CSPACE_TEST_ATTACH_SIGNAL_HELPER=1")
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			waited := false
			helperPID := 0
			t.Cleanup(func() {
				// Only the two processes this test started are eligible for
				// cleanup. A passing wrapper has already reaped its child.
				if helperPID > 0 {
					if process, err := os.FindProcess(helperPID); err == nil {
						_ = process.Kill()
					}
				}
				if !waited {
					_ = cmd.Process.Kill()
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("signal-test wrapper did not reap after cleanup")
					}
				}
			})
			deadline := time.Now().Add(5 * time.Second)
			for helperPID == 0 && time.Now().Before(deadline) {
				if raw, err := os.ReadFile(filepath.Join(dir, "ready")); err == nil {
					helperPID, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
				}
				if helperPID == 0 {
					select {
					case err := <-done:
						waited = true
						t.Fatalf("wrapper exited before child was ready: %v\n%s", err, output.String())
					case <-time.After(10 * time.Millisecond):
					}
				}
			}
			if helperPID <= 0 {
				t.Fatal("signal-ignoring child never became ready")
			}
			started := time.Now()
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				waited = true
				if err != nil {
					t.Fatalf("wrapper exited before attach cleanup: %v\n%s", err, output.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("runAttachChild stayed blocked after its child ignored shutdown")
			}
			helperPID = 0 // The successful wrapper reaped it; never signal a reused PID.
			raw, err := os.ReadFile(filepath.Join(dir, "completed"))
			if err != nil || string(raw) != "code=-1 err=<nil>\n" {
				t.Fatalf("attach did not return after force-killing its child: %q, %v", raw, err)
			}
			if elapsed := time.Since(started); elapsed < 1500*time.Millisecond {
				t.Fatalf("child exited after %s without allowing the shutdown grace period", elapsed)
			}
		})
	}
}

func TestRunAttachSignalHelper(t *testing.T) {
	if os.Getenv("CSPACE_TEST_ATTACH_SIGNAL_HELPER") != "1" {
		return
	}
	mode, dir := os.Args[len(os.Args)-2], os.Args[len(os.Args)-1]
	if mode == "ignore" {
		signal.Ignore(syscall.SIGTERM, syscall.SIGHUP)
		if err := os.WriteFile(filepath.Join(dir, "ready"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(2)
		}
		for {
			time.Sleep(time.Hour)
		}
	}
	if mode != "wrapper" {
		os.Exit(2)
	}
	code, err := runAttachChild(os.Args[0], []string{"cli.test", "-test.run=^TestRunAttachSignalHelper$", "--", "ignore", dir})
	result := fmt.Sprintf("code=%d err=%v\n", code, err)
	if err := os.WriteFile(filepath.Join(dir, "completed"), []byte(result), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
