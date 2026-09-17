//! Thin attach client: connects to the daemon's socket, sends an Attach
//! frame, then relays bytes between the daemon and a local vt100 parser +
//! tui-term-rendered ratatui TUI. All terminal emulation happens here,
//! client-side — the daemon never touches vt100 at all.

use std::io;
use std::os::unix::net::UnixStream;
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use ratatui::crossterm::event::{self, Event, KeyCode, KeyEventKind, KeyModifiers};
use ratatui::crossterm::execute;
use ratatui::crossterm::terminal::{disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen};
use ratatui::style::Style;
use ratatui::widgets::{Block, Borders};
use tui_term::widget::PseudoTerminal;

use crate::protocol::{self, TAG_ATTACHED, TAG_INPUT, TAG_OUTPUT, TAG_RESIZE};

const SCROLLBACK_LINES: usize = 10_000;

pub fn run(socket_path: &Path, pane_name: &str) -> io::Result<()> {
    let mut stream = UnixStream::connect(socket_path)?;
    protocol::write_frame(&mut stream, protocol::TAG_ATTACH, pane_name.as_bytes())?;
    match protocol::read_frame(&mut stream)? {
        Some(f) if f.tag == TAG_ATTACHED => {}
        _ => return Err(io::Error::other("daemon did not confirm attach")),
    }

    let parser = Arc::new(Mutex::new(vt100::Parser::new(24, 80, SCROLLBACK_LINES)));
    let mut writer = stream.try_clone()?;

    // Reader thread: daemon Output frames -> vt100 parser. Mirrors
    // mux-prototype's reader thread, just fed by the socket instead of a
    // local PTY reader.
    let reader_parser = Arc::clone(&parser);
    let mut reader_stream = stream.try_clone()?;
    std::thread::spawn(move || loop {
        match protocol::read_frame(&mut reader_stream) {
            Ok(Some(f)) if f.tag == TAG_OUTPUT => {
                reader_parser.lock().unwrap().process(&f.payload);
            }
            Ok(Some(_)) => {}
            Ok(None) | Err(_) => break, // daemon gone or socket closed
        }
    });

    enable_raw_mode()?;
    execute!(io::stdout(), EnterAlternateScreen)?;
    let mut terminal = ratatui::init();
    let mut last_size = (0u16, 0u16);

    let result = (|| -> io::Result<()> {
        loop {
            terminal.draw(|frame| {
                let area = frame.area();
                let inner_rows = area.height.saturating_sub(2);
                let inner_cols = area.width.saturating_sub(2);
                if (inner_rows, inner_cols) != last_size && inner_rows > 0 && inner_cols > 0 {
                    last_size = (inner_rows, inner_cols);
                    let _ = protocol::write_frame(
                        &mut writer,
                        TAG_RESIZE,
                        &protocol::encode_resize(inner_rows, inner_cols),
                    );
                    parser.lock().unwrap().screen_mut().set_size(inner_rows, inner_cols);
                }
                let parser = parser.lock().unwrap();
                let screen = parser.screen();
                let title = format!(" {pane_name} — daemon-backed (detach: close this window; the pane keeps running) ");
                let widget = PseudoTerminal::new(screen)
                    .block(Block::default().title(title).borders(Borders::ALL).border_style(Style::default()));
                frame.render_widget(widget, area);
            })?;

            if !event::poll(Duration::from_millis(33))? {
                continue;
            }
            if let Event::Key(key) = event::read()? {
                if key.kind != KeyEventKind::Press {
                    continue;
                }
                if key.modifiers.contains(KeyModifiers::CONTROL) && key.code == KeyCode::Char('q') {
                    break; // detach: just stop this client, the daemon keeps the pane alive
                }
                if let Some(bytes) = key_to_bytes(key.code, key.modifiers) {
                    protocol::write_frame(&mut writer, TAG_INPUT, &bytes)?;
                }
            }
        }
        Ok(())
    })();

    ratatui::restore();
    execute!(io::stdout(), LeaveAlternateScreen)?;
    disable_raw_mode()?;
    result
}

fn key_to_bytes(code: KeyCode, mods: KeyModifiers) -> Option<Vec<u8>> {
    if mods.contains(KeyModifiers::CONTROL)
        && let KeyCode::Char(c) = code
    {
        let c = c.to_ascii_lowercase();
        if c.is_ascii_lowercase() {
            return Some(vec![c as u8 - b'a' + 1]);
        }
    }
    match code {
        KeyCode::Char(c) => Some(c.to_string().into_bytes()),
        KeyCode::Enter => Some(b"\r".to_vec()),
        KeyCode::Backspace => Some(b"\x7f".to_vec()),
        KeyCode::Tab => Some(b"\t".to_vec()),
        KeyCode::Esc => Some(b"\x1b".to_vec()),
        KeyCode::Up => Some(b"\x1b[A".to_vec()),
        KeyCode::Down => Some(b"\x1b[B".to_vec()),
        KeyCode::Right => Some(b"\x1b[C".to_vec()),
        KeyCode::Left => Some(b"\x1b[D".to_vec()),
        _ => None,
    }
}

