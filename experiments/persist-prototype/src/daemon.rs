//! The persistence proof: a daemon that owns PTYs independent of any
//! attached client. A client attaching/detaching (or crashing, or its
//! terminal window closing) never touches the panes it doesn't own — only
//! the daemon does, and the daemon outlives the terminal that started it
//! via `daemonize::detach` (fork + setsid + ignore SIGHUP).

use std::collections::HashMap;
use std::io::{Read, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::Path;
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};

use portable_pty::{native_pty_system, Child, CommandBuilder, MasterPty, PtySize};

use crate::protocol::{self, TAG_ATTACH, TAG_ATTACHED, TAG_INPUT, TAG_OUTPUT, TAG_RESIZE};

struct DaemonPane {
    master: Mutex<Box<dyn MasterPty + Send>>,
    writer: Mutex<Box<dyn Write + Send>>,
    // Kept alive for its Drop impl / so we could add explicit kill later;
    // not read directly, same rationale as mux-prototype's Pane::child.
    _child: Mutex<Box<dyn Child + Send + Sync>>,
    /// The currently-attached client's socket, if any. Only one client can
    /// be attached at a time in this prototype; attaching replaces
    /// whichever client held it before (tmux without `-d` protection).
    subscriber: Mutex<Option<UnixStream>>,
    /// Guards against a just-superseded connection's cleanup clobbering a
    /// newer attach: each attach bumps this; a handler only clears
    /// `subscriber` on exit if its own generation is still current.
    generation: AtomicU64,
}

type Registry = Arc<Mutex<HashMap<String, Arc<DaemonPane>>>>;

fn get_or_create_pane(registry: &Registry, name: &str) -> io::Result<Arc<DaemonPane>> {
    let mut map = registry.lock().unwrap();
    if let Some(pane) = map.get(name) {
        return Ok(Arc::clone(pane));
    }

    let pty_system = native_pty_system();
    let pair = pty_system
        .openpty(PtySize { rows: 24, cols: 80, pixel_width: 0, pixel_height: 0 })
        .map_err(io::Error::other)?;
    let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/bash".to_string());
    let child = pair.slave.spawn_command(CommandBuilder::new(shell)).map_err(io::Error::other)?;
    drop(pair.slave);

    let reader = pair.master.try_clone_reader().map_err(io::Error::other)?;
    let writer = pair.master.take_writer().map_err(io::Error::other)?;

    let pane = Arc::new(DaemonPane {
        master: Mutex::new(pair.master),
        writer: Mutex::new(writer),
        _child: Mutex::new(child),
        subscriber: Mutex::new(None),
        generation: AtomicU64::new(0),
    });

    // The one thread that lives for the pane's whole lifetime, independent
    // of any client connection: relay PTY output to whichever client is
    // currently attached, or drop it on the floor if none is (accepted
    // simplification — see experiments/README.md's persistence section).
    let reader_pane = Arc::clone(&pane);
    std::thread::spawn(move || {
        let mut reader = reader;
        let mut buf = [0u8; 8192];
        loop {
            match reader.read(&mut buf) {
                Ok(0) | Err(_) => break,
                Ok(n) => {
                    let mut sub = reader_pane.subscriber.lock().unwrap();
                    if let Some(stream) = sub.as_mut()
                        && protocol::write_frame(stream, TAG_OUTPUT, &buf[..n]).is_err() {
                            *sub = None;
                        }
                }
            }
        }
    });

    map.insert(name.to_string(), Arc::clone(&pane));
    Ok(pane)
}

fn handle_connection(mut stream: UnixStream, registry: Registry) -> io::Result<()> {
    let first = match protocol::read_frame(&mut stream)? {
        Some(f) if f.tag == TAG_ATTACH => f,
        _ => return Ok(()), // not an attach, nothing to do
    };
    let name = String::from_utf8_lossy(&first.payload).to_string();
    let pane = get_or_create_pane(&registry, &name)?;

    let my_generation = pane.generation.fetch_add(1, Ordering::SeqCst) + 1;
    *pane.subscriber.lock().unwrap() = Some(stream.try_clone()?);
    protocol::write_frame(&mut stream, TAG_ATTACHED, &[1])?;

    // Loop ends on read_frame returning None: client detached (or its
    // terminal closed) — the pane lives on regardless.
    while let Some(frame) = protocol::read_frame(&mut stream)? {
        match frame.tag {
            TAG_INPUT => {
                let _ = pane.writer.lock().unwrap().write_all(&frame.payload);
            }
            TAG_RESIZE => {
                if let Some((rows, cols)) = protocol::decode_resize(&frame.payload) {
                    let _ = pane.master.lock().unwrap().resize(PtySize {
                        rows,
                        cols,
                        pixel_width: 0,
                        pixel_height: 0,
                    });
                }
            }
            _ => {}
        }
    }

    // Only clear the subscriber if nobody newer has attached since us.
    if pane.generation.load(Ordering::SeqCst) == my_generation {
        *pane.subscriber.lock().unwrap() = None;
    }
    Ok(())
}

use std::io;

/// Where a test (or an operator) can find the real post-fork daemon PID —
/// `Command::spawn()`'s own `Child::id()` is useless here, since after a
/// detaching fork that intermediate process has already exited.
pub fn pid_file_path(socket_path: &Path) -> std::path::PathBuf {
    let mut s = socket_path.as_os_str().to_owned();
    s.push(".pid");
    std::path::PathBuf::from(s)
}

pub fn serve(socket_path: &Path) -> io::Result<()> {
    if socket_path.exists() {
        std::fs::remove_file(socket_path)?;
    }
    let listener = UnixListener::bind(socket_path)?;
    std::fs::write(pid_file_path(socket_path), std::process::id().to_string())?;
    let registry: Registry = Arc::new(Mutex::new(HashMap::new()));

    for stream in listener.incoming() {
        let stream = stream?;
        let registry = Arc::clone(&registry);
        std::thread::spawn(move || {
            let _ = handle_connection(stream, registry);
        });
    }
    Ok(())
}
