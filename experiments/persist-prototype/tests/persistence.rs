//! Integration tests that drive the real compiled binary as a real
//! separate OS process — deliberately not in-process/unit tests, since
//! `fork()` inside a multithreaded test harness is a known footgun
//! (Rust's test runner is multithreaded; fork only duplicates the calling
//! thread, leaving other threads' locks in a possibly-inconsistent state
//! in the child). Driving the real binary as a subprocess is both safer
//! and a more honest proof: it's exactly how a user would exercise this.

#[path = "../src/protocol.rs"]
#[allow(dead_code)] // resize framing exists for the real client; unused by these tests
mod protocol;

use std::os::unix::net::UnixStream;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use nix::sys::signal::{kill, Signal};
use nix::unistd::Pid;

fn unique_socket_path(test_name: &str) -> PathBuf {
    std::env::temp_dir().join(format!(
        "cspace-persist-proto-test-{test_name}-{}.sock",
        std::process::id()
    ))
}

fn spawn_daemon(socket: &Path) {
    // The spawned process forks and its parent branch exits immediately
    // (see daemonize::detach) — wait() reaps that short-lived intermediate
    // process so it doesn't linger as a zombie; the real daemon is by then
    // an independent, re-parented process this handle no longer tracks.
    Command::new(env!("CARGO_BIN_EXE_persist-prototype"))
        .arg("daemon")
        .env("PERSIST_PROTO_SOCKET", socket)
        .spawn()
        .expect("spawn daemon subprocess")
        .wait()
        .expect("wait on daemon-launching process");

    let deadline = Instant::now() + Duration::from_secs(3);
    while Instant::now() < deadline {
        if UnixStream::connect(socket).is_ok() {
            return;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    panic!("daemon socket never came up at {socket:?}");
}

fn daemon_pid(socket: &Path) -> i32 {
    let pid_file = {
        let mut s = socket.as_os_str().to_owned();
        s.push(".pid");
        PathBuf::from(s)
    };
    std::fs::read_to_string(pid_file)
        .expect("read daemon pid file")
        .trim()
        .parse()
        .expect("daemon pid file contains a valid pid")
}

fn cleanup(socket: &Path, pid: i32) {
    let _ = kill(Pid::from_raw(pid), Signal::SIGKILL);
    let _ = std::fs::remove_file(socket);
    let mut pid_file = socket.as_os_str().to_owned();
    pid_file.push(".pid");
    let _ = std::fs::remove_file(pid_file);
}

/// A raw protocol client used only by the tests, deliberately independent
/// of `client.rs`'s ratatui/vt100 rendering — this exercises exactly the
/// wire behavior a real attach client relies on, without needing a TTY.
struct RawClient {
    stream: UnixStream,
    received: Arc<Mutex<Vec<u8>>>,
}

impl RawClient {
    fn attach(socket: &PathBuf, pane: &str) -> Self {
        let mut stream = UnixStream::connect(socket).expect("connect to daemon");
        protocol::write_frame(&mut stream, protocol::TAG_ATTACH, pane.as_bytes())
            .expect("send attach frame");
        match protocol::read_frame(&mut stream).expect("read attach reply") {
            Some(f) if f.tag == protocol::TAG_ATTACHED => {}
            other => panic!("expected Attached frame, got {other:?}"),
        }

        let received = Arc::new(Mutex::new(Vec::new()));
        let reader_received = Arc::clone(&received);
        let mut reader_stream = stream.try_clone().expect("clone stream for reader");
        std::thread::spawn(move || loop {
            match protocol::read_frame(&mut reader_stream) {
                Ok(Some(f)) if f.tag == protocol::TAG_OUTPUT => {
                    reader_received.lock().unwrap().extend_from_slice(&f.payload);
                }
                Ok(Some(_)) => {}
                Ok(None) | Err(_) => break,
            }
        });

        Self { stream, received }
    }

    fn send(&mut self, text: &str) {
        protocol::write_frame(&mut self.stream, protocol::TAG_INPUT, text.as_bytes())
            .expect("send input frame");
    }

    /// Polls until `marker` is followed by at least one digit somewhere in
    /// the accumulated output, returning those digits. A PTY echoes back
    /// whatever you type before the shell executes it, so a naive
    /// substring search on `marker` alone matches the echoed *command*
    /// line too (e.g. `printf 'PID=%s\n' $$` echoed verbatim, where
    /// "PID=" is immediately followed by "%s", not a digit) — this skips
    /// any such occurrence and keeps scanning for the real output.
    fn wait_for_digits_after(&self, marker: &str, timeout: Duration) -> String {
        let deadline = Instant::now() + timeout;
        while Instant::now() < deadline {
            let digits = extract_after(&self.received.lock().unwrap(), marker);
            if !digits.is_empty() {
                return digits;
            }
            std::thread::sleep(Duration::from_millis(50));
        }
        String::new()
    }
}

impl std::fmt::Debug for protocol::Frame {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Frame").field("tag", &self.tag).field("len", &self.payload.len()).finish()
    }
}

/// The core persistence claim: a pane's shell process survives a client
/// disconnecting and a *different* client reconnecting later — proven not
/// by inference but by asking the shell for its own PID before and after,
/// which only matches if it's the same live process both times.
#[test]
fn pane_survives_client_disconnect_and_reattach() {
    let socket = unique_socket_path("reattach");
    let _ = std::fs::remove_file(&socket);
    spawn_daemon(&socket);
    let pid = daemon_pid(&socket);

    let pane = "proof-pane";

    // First client: ask the shell its own PID, then disconnect (simulates
    // closing the terminal window without killing the daemon).
    let first_pid = {
        let mut client = RawClient::attach(&socket, pane);
        client.send("printf 'FIRST_PID=%s\\n' $$\n");
        client.wait_for_digits_after("FIRST_PID=", Duration::from_secs(5))
    };

    // The first client's socket goes out of scope here and is dropped —
    // this is the "close the terminal" moment. No explicit detach message
    // exists in this protocol; EOF on the socket is the only signal, same
    // as a real terminal closing.

    std::thread::sleep(Duration::from_millis(300));

    // Second, independent client attaches to the SAME pane name.
    let second_pid = {
        let mut client = RawClient::attach(&socket, pane);
        client.send("printf 'SECOND_PID=%s\\n' $$\n");
        client.wait_for_digits_after("SECOND_PID=", Duration::from_secs(5))
    };

    cleanup(&socket, pid);

    assert!(!first_pid.is_empty(), "failed to parse first shell PID from output");
    assert_eq!(
        first_pid, second_pid,
        "shell PID changed across reattach — a NEW shell was spawned instead of \
         reconnecting to the surviving one, persistence did not hold"
    );
}

/// Proves the actual OS mechanism, not just its effect: the daemon
/// genuinely detached (setsid) and ignores SIGHUP, so a signal that would
/// have killed a normal foreground child of the test process does not
/// kill it.
#[test]
fn daemon_survives_sighup() {
    let socket = unique_socket_path("sighup");
    let _ = std::fs::remove_file(&socket);
    spawn_daemon(&socket);
    let pid = daemon_pid(&socket);

    kill(Pid::from_raw(pid), Signal::SIGHUP).expect("send SIGHUP to daemon");
    std::thread::sleep(Duration::from_millis(200));

    // If SIGHUP had killed it (the default disposition for a process that
    // hadn't detached), this connect would fail.
    let still_alive = UnixStream::connect(&socket).is_ok();

    cleanup(&socket, pid);

    assert!(still_alive, "daemon did not survive SIGHUP — detach did not take effect");
}

fn extract_after(buf: &[u8], marker: &str) -> String {
    let text = String::from_utf8_lossy(buf);
    let mut search_from = 0;
    while let Some(rel) = text[search_from..].find(marker) {
        let after = search_from + rel + marker.len();
        let digits: String = text[after..].chars().take_while(|c| c.is_ascii_digit()).collect();
        if !digits.is_empty() {
            return digits;
        }
        search_from = after;
    }
    String::new()
}
