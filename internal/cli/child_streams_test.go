package cli

import (
	"bytes"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// nonblock reports whether the file's open file description carries
// O_NONBLOCK, which is what a child like `container exec -it` leaves behind.
func nonblock(t *testing.T, f *os.File) bool {
	t.Helper()
	var flags int
	var ctlErr error
	conn, err := f.SyscallConn()
	if err != nil {
		t.Fatalf("SyscallConn: %v", err)
	}
	if err := conn.Control(func(fd uintptr) {
		flags, ctlErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
	}); err != nil {
		t.Fatalf("Control: %v", err)
	}
	if ctlErr != nil {
		t.Fatalf("F_GETFL: %v", ctlErr)
	}
	return flags&syscall.O_NONBLOCK != 0
}

func TestRestoreBlockingStreamsClearsNonblock(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	for _, f := range []*os.File{r, w} {
		conn, err := f.SyscallConn()
		if err != nil {
			t.Fatalf("SyscallConn: %v", err)
		}
		if err := conn.Control(func(fd uintptr) {
			if err := syscall.SetNonblock(int(fd), true); err != nil {
				t.Errorf("SetNonblock: %v", err)
			}
		}); err != nil {
			t.Fatalf("Control: %v", err)
		}
	}
	if !nonblock(t, w) {
		t.Fatalf("precondition: the write end should be non-blocking")
	}

	restoreBlockingStreams(r, w)

	if nonblock(t, r) {
		t.Errorf("read end is still non-blocking")
	}
	if nonblock(t, w) {
		t.Errorf("write end is still non-blocking")
	}
}

// A stream os/exec gave its own pipe is not a descriptor of ours, and a nil
// one is not a descriptor at all. Neither may take the call down: this runs
// on every attach's way out, including its error paths.
func TestRestoreBlockingStreamsIgnoresWhatIsNotAFile(t *testing.T) {
	var missing *os.File
	restoreBlockingStreams(nil, missing, &bytes.Buffer{}, bytes.NewReader(nil))
}
