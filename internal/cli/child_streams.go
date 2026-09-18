package cli

import (
	"os"
	"syscall"
)

// restoreBlockingStreams puts the terminal file descriptors a foreground
// child was handed back into blocking mode.
//
// `container exec -it` drives the tty with async I/O and leaves O_NONBLOCK
// set on it when it exits. The flag lives on the open file description, not
// on the descriptor, and the child shares ours — so it outlives the child and
// applies to every write cspace (and the shell that started it) makes to that
// terminal afterwards.
//
// Go's os package assumes the standard descriptors are blocking and does not
// register them with the runtime poller, so a write that does not fit in the
// tty's buffer comes back short with EAGAIN instead of being retried. In
// `cspace tui` that lands on bubbletea's post-attach repaint: it drops the
// error (`_ = p.renderer.flush(false)`) while its renderer still records the
// frame as painted, so whatever was cut off is never drawn again and the
// dashboard keeps a half-blank screen for the rest of the session. See
// .cspace/context/findings/2026-09-18-dashboard-screen-comes-back-blank-after-attach.md.
//
// Anything that is not an *os.File is ignored: os/exec gives such a stream
// its own pipe, so the child never touches our descriptor.
func restoreBlockingStreams(streams ...any) {
	for _, s := range streams {
		f, ok := s.(*os.File)
		if !ok || f == nil {
			continue
		}
		// SyscallConn rather than Fd(): Control hands over the descriptor
		// without detaching the file from the runtime poller.
		conn, err := f.SyscallConn()
		if err != nil {
			continue
		}
		_ = conn.Control(func(fd uintptr) { _ = syscall.SetNonblock(int(fd), false) })
	}
}
