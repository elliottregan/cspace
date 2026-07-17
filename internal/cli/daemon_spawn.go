package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func daemonLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".cspace")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.log"), nil
}

// newDaemonCommand builds a detached `<self> daemon serve`. Setsid detaches it
// from the parent's session; stdout+stderr go to an append-only log FILE (not
// a parent-held os.Pipe) so a later log write can't take EPIPE -> SIGPIPE after
// cspace up exits. Caller closes the returned file after Start.
func newDaemonCommand(self string) (*exec.Cmd, *os.File, error) {
	logPath, err := daemonLogPath()
	if err != nil {
		return nil, nil, err
	}
	if err := rotateIfLarge(logPath, 1<<20); err != nil { // 1 MiB cap (spec 1a)
		return nil, nil, err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.Command(self, "daemon", "serve")
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, f, nil
}

// rotateIfLarge truncates the log by renaming to .1 once it exceeds max bytes,
// bounding the gateway-retry chatter over long-lived hosts.
func rotateIfLarge(path string, max int64) error {
	if fi, err := os.Stat(path); err == nil && fi.Size() > max {
		return os.Rename(path, path+".1")
	}
	return nil
}
