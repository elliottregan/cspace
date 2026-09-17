//! The actual mechanism that makes persistence possible: detach this
//! process from whatever terminal/session started it, so that terminal
//! closing (which delivers SIGHUP to its session's foreground process
//! group) can never reach us.
//!
//! This is a single fork, not a textbook double-fork. A double fork's
//! extra guarantee (preventing the daemon from ever reacquiring a
//! controlling terminal under SVR4 TTY semantics) isn't load-bearing on
//! modern Linux/macOS with devpts — single fork + setsid + ignoring SIGHUP
//! is what most lightweight daemonizing code actually relies on. Noted
//! here rather than silently assumed, since it's a real simplification.
//!
//! Must run before any threads are spawned or the ratatui/portable-pty
//! machinery touches anything — `fork()` in a multithreaded process only
//! duplicates the calling thread, leaving any other thread's held locks
//! (allocator included) in a potentially inconsistent state in the child.
//! This is why `main()` calls this as its very first action.

use std::fs::OpenOptions;
use std::io;
use std::process::exit;

use nix::sys::signal::{signal, SigHandler, Signal};
use nix::unistd::{dup2_stderr, dup2_stdin, dup2_stdout, fork, setsid, ForkResult};

/// Forks, detaches the child into its own session, ignores SIGHUP, and
/// redirects stdio to `/dev/null`. Returns only in the child; the parent
/// (the original foreground process the shell is waiting on) exits
/// immediately so the invoking shell gets its prompt back right away.
pub fn detach() -> io::Result<()> {
    unsafe {
        // Ignore SIGHUP in both parent and child as defense in depth —
        // belt-and-suspenders alongside setsid() actually leaving the
        // session that would have delivered it.
        signal(Signal::SIGHUP, SigHandler::SigIgn).map_err(io::Error::from)?;
    }

    match unsafe { fork() }.map_err(io::Error::from)? {
        ForkResult::Parent { .. } => exit(0),
        ForkResult::Child => {}
    }

    // Leave the parent's session entirely — this is the actual mechanism:
    // a closed terminal sends SIGHUP to its session's foreground process
    // group, and we are no longer a member of that session at all.
    setsid().map_err(io::Error::from)?;

    let dev_null_read = OpenOptions::new().read(true).open("/dev/null")?;
    let dev_null_write = OpenOptions::new().write(true).open("/dev/null")?;
    dup2_stdin(&dev_null_read).map_err(io::Error::from)?;
    dup2_stdout(&dev_null_write).map_err(io::Error::from)?;
    dup2_stderr(&dev_null_write).map_err(io::Error::from)?;
    // dev_null_{read,write} drop here; the fds we duplicated onto 0/1/2
    // stay open independently of these File handles.

    Ok(())
}
