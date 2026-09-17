//! Persistence prototype: proves that a pane's process can outlive the
//! TUI client attached to it, using a daemon that self-detaches from the
//! invoking terminal (fork + setsid + ignore SIGHUP) rather than adopting
//! a heavyweight multiplexer engine like rmux. See
//! experiments/README.md's "Persistence" section for the design writeup.
//!
//! Usage:
//!   persist-prototype daemon              (usually invoked via `--foreground`
//!                                           for testing; auto-spawned by
//!                                           `attach` otherwise)
//!   persist-prototype attach <pane-name>  (creates the pane if it doesn't
//!                                           exist yet, spawning the daemon
//!                                           first if none is running)

mod client;
mod daemon;
mod daemonize;
mod protocol;

use std::io;
use std::path::{Path, PathBuf};
use std::time::Duration;

fn socket_path() -> PathBuf {
    if let Ok(p) = std::env::var("PERSIST_PROTO_SOCKET") {
        return PathBuf::from(p);
    }
    std::env::temp_dir().join("cspace-persist-proto.sock")
}

/// Spawns the daemon (detached) if the socket isn't already live, then
/// waits briefly for it to start accepting connections.
fn ensure_daemon_running(socket_path: &Path) -> io::Result<()> {
    if std::os::unix::net::UnixStream::connect(socket_path).is_ok() {
        return Ok(()); // already running
    }

    let exe = std::env::current_exe()?;
    std::process::Command::new(exe).arg("daemon").spawn()?;

    for _ in 0..50 {
        if std::os::unix::net::UnixStream::connect(socket_path).is_ok() {
            return Ok(());
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    Err(io::Error::other("daemon did not come up within 2.5s"))
}

fn main() -> io::Result<()> {
    let args: Vec<String> = std::env::args().collect();
    let socket = socket_path();

    match args.get(1).map(String::as_str) {
        Some("daemon") => {
            if args.get(2).map(String::as_str) != Some("--foreground") {
                daemonize::detach()?;
            }
            daemon::serve(&socket)
        }
        Some("attach") => {
            let name = args.get(2).cloned().unwrap_or_else(|| "sandbox-1".to_string());
            ensure_daemon_running(&socket)?;
            client::run(&socket, &name)
        }
        _ => {
            eprintln!("usage: persist-prototype daemon [--foreground] | attach <pane-name>");
            std::process::exit(2);
        }
    }
}
