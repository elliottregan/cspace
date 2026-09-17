//! Prototype: a herdr-style terminal multiplexer built on
//! portable-pty (spawn/own each child's PTY) + vt100 (terminal emulation)
//! + tui-term (renders a vt100::Screen as a ratatui widget).
//!
//! Panes here spawn a local shell as a stand-in for `container exec -it
//! <sandbox> claude` — this environment has no Apple Container, so this
//! prototype validates the multiplexing mechanics (spawn, render, resize,
//! input routing, focus switching) rather than real cspace integration.
//!
//! Controls:
//!   Ctrl+b then n / p   next / previous pane
//!   Ctrl+b then t       new pane (spawns another shell)
//!   Ctrl+b then x       kill focused pane
//!   Ctrl+b then c       cycle focused pane's demo status (stand-in for
//!                       real status derived from cspace's events.ndjson)
//!   Ctrl+b then q       quit
//!   anything else       forwarded to the focused pane's shell

use std::io::{self, Read, Write};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use portable_pty::{native_pty_system, Child, CommandBuilder, MasterPty, PtySize};
use ratatui::crossterm::event::{
    self, DisableBracketedPaste, EnableBracketedPaste, Event, KeyCode, KeyEventKind, KeyModifiers,
};
use ratatui::crossterm::execute;
use ratatui::crossterm::terminal::{
    disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen,
};
use ratatui::layout::{Constraint, Direction, Layout, Rect};
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, List, ListItem, ListState, Tabs};
use ratatui::{DefaultTerminal, Frame};
use tui_term::widget::PseudoTerminal;

#[derive(Clone, Copy, PartialEq, Eq)]
enum PaneStatus {
    Working,
    Idle,
    Blocked,
    Done,
}

impl PaneStatus {
    fn next(self) -> Self {
        match self {
            PaneStatus::Working => PaneStatus::Blocked,
            PaneStatus::Blocked => PaneStatus::Idle,
            PaneStatus::Idle => PaneStatus::Done,
            PaneStatus::Done => PaneStatus::Working,
        }
    }

    fn glyph(self) -> &'static str {
        match self {
            PaneStatus::Working => "●",
            PaneStatus::Idle => "○",
            PaneStatus::Blocked => "▲",
            PaneStatus::Done => "✓",
        }
    }

    fn color(self) -> Color {
        match self {
            PaneStatus::Working => Color::Green,
            PaneStatus::Idle => Color::DarkGray,
            PaneStatus::Blocked => Color::Yellow,
            PaneStatus::Done => Color::Blue,
        }
    }
}

/// One pane: owns the child's PTY master + writer, and the vt100 parser
/// that a background thread feeds from the PTY's byte stream. tui-term
/// only ever sees `parser.screen()` at render time — it never touches
/// the raw bytes itself.
struct Pane {
    title: String,
    status: PaneStatus,
    master: Box<dyn MasterPty + Send>,
    writer: Box<dyn Write + Send>,
    parser: Arc<Mutex<vt100::Parser>>,
    child: Box<dyn Child + Send + Sync>,
    last_size: (u16, u16), // (rows, cols) last sent to the PTY + parser
}

fn spawn_pane(title: impl Into<String>, rows: u16, cols: u16) -> io::Result<Pane> {
    let pty_system = native_pty_system();
    let pair = pty_system
        .openpty(PtySize {
            rows,
            cols,
            pixel_width: 0,
            pixel_height: 0,
        })
        .map_err(io::Error::other)?;

    let shell = std::env::var("SHELL").unwrap_or_else(|_| "/bin/bash".to_string());
    let cmd = CommandBuilder::new(shell);
    let child = pair
        .slave
        .spawn_command(cmd)
        .map_err(io::Error::other)?;
    // The slave end belongs to the child now; drop our copy so the
    // master's reader sees EOF when the child actually exits.
    drop(pair.slave);

    let reader = pair
        .master
        .try_clone_reader()
        .map_err(io::Error::other)?;
    let writer = pair.master.take_writer().map_err(io::Error::other)?;

    let parser = Arc::new(Mutex::new(vt100::Parser::new(rows, cols, 0)));

    let reader_parser = Arc::clone(&parser);
    std::thread::spawn(move || {
        let mut reader = reader;
        let mut buf = [0u8; 8192];
        loop {
            match reader.read(&mut buf) {
                Ok(0) => break, // child exited
                Ok(n) => reader_parser.lock().unwrap().process(&buf[..n]),
                Err(_) => break,
            }
        }
    });

    Ok(Pane {
        title: title.into(),
        status: PaneStatus::Working,
        master: pair.master,
        writer,
        parser,
        child,
        last_size: (rows, cols),
    })
}

impl Pane {
    fn resize(&mut self, rows: u16, cols: u16) {
        if rows == 0 || cols == 0 || self.last_size == (rows, cols) {
            return;
        }
        let _ = self.master.resize(PtySize {
            rows,
            cols,
            pixel_width: 0,
            pixel_height: 0,
        });
        self.parser.lock().unwrap().screen_mut().set_size(rows, cols);
        self.last_size = (rows, cols);
    }

    fn write_bytes(&mut self, bytes: &[u8]) {
        let _ = self.writer.write_all(bytes);
    }

    fn is_alive(&mut self) -> bool {
        matches!(self.child.try_wait(), Ok(None))
    }
}

struct App {
    panes: Vec<Pane>,
    focused: usize,
    awaiting_leader: bool,
    next_id: usize,
}

impl App {
    fn new() -> io::Result<Self> {
        let first = spawn_pane("sandbox-1", 24, 80)?;
        Ok(Self {
            panes: vec![first],
            focused: 0,
            awaiting_leader: false,
            next_id: 2,
        })
    }

    fn new_pane(&mut self) {
        if let Ok(p) = spawn_pane(format!("sandbox-{}", self.next_id), 24, 80) {
            self.next_id += 1;
            self.panes.push(p);
            self.focused = self.panes.len() - 1;
        }
    }

    fn kill_focused(&mut self) {
        if self.panes.len() <= 1 {
            return; // keep at least one pane in this prototype
        }
        self.panes.remove(self.focused);
        if self.focused >= self.panes.len() {
            self.focused = self.panes.len() - 1;
        }
    }

    fn next_pane(&mut self) {
        if !self.panes.is_empty() {
            self.focused = (self.focused + 1) % self.panes.len();
        }
    }

    fn prev_pane(&mut self) {
        if !self.panes.is_empty() {
            self.focused = (self.focused + self.panes.len() - 1) % self.panes.len();
        }
    }

    fn reap_dead(&mut self) {
        self.panes.retain_mut(|p| p.is_alive());
        if self.panes.is_empty() {
            return;
        }
        if self.focused >= self.panes.len() {
            self.focused = self.panes.len() - 1;
        }
    }

    fn draw(&mut self, frame: &mut Frame) {
        let root = Layout::default()
            .direction(Direction::Horizontal)
            .constraints([Constraint::Length(24), Constraint::Min(1)])
            .split(frame.area());

        self.draw_sidebar(frame, root[0]);

        let main = Layout::default()
            .direction(Direction::Vertical)
            .constraints([Constraint::Length(3), Constraint::Min(1)])
            .split(root[1]);

        self.draw_tabs(frame, main[0]);
        self.draw_focused_pane(frame, main[1]);
    }

    fn draw_sidebar(&self, frame: &mut Frame, area: Rect) {
        let items: Vec<ListItem> = self
            .panes
            .iter()
            .map(|p| {
                let line = Line::from(vec![
                    Span::styled(format!("{} ", p.status.glyph()), Style::default().fg(p.status.color())),
                    Span::raw(p.title.clone()),
                ]);
                ListItem::new(line)
            })
            .collect();

        let mut state = ListState::default().with_selected(Some(self.focused));
        let list = List::new(items)
            .block(Block::default().title(" sandboxes ").borders(Borders::ALL))
            .highlight_style(Style::default().add_modifier(Modifier::BOLD | Modifier::REVERSED));

        frame.render_stateful_widget(list, area, &mut state);
    }

    fn draw_tabs(&self, frame: &mut Frame, area: Rect) {
        let titles: Vec<Line> = self.panes.iter().map(|p| Line::from(p.title.clone())).collect();
        let tabs = Tabs::new(titles)
            .block(Block::default().borders(Borders::ALL))
            .select(self.focused)
            .highlight_style(Style::default().fg(Color::Black).bg(Color::Cyan));
        frame.render_widget(tabs, area);
    }

    fn draw_focused_pane(&mut self, frame: &mut Frame, area: Rect) {
        // Leave room for the border: match the PTY to the interior size.
        let inner_rows = area.height.saturating_sub(2);
        let inner_cols = area.width.saturating_sub(2);
        if let Some(pane) = self.panes.get_mut(self.focused) {
            pane.resize(inner_rows, inner_cols);
            let parser = pane.parser.lock().unwrap();
            let screen = parser.screen();
            let title = format!(" {} — {:?} (Ctrl+b then ? for help) ", pane.title, status_label(pane.status));
            let widget = PseudoTerminal::new(screen).block(Block::default().title(title).borders(Borders::ALL));
            frame.render_widget(widget, area);
        }
    }
}

fn status_label(s: PaneStatus) -> &'static str {
    match s {
        PaneStatus::Working => "working",
        PaneStatus::Idle => "idle",
        PaneStatus::Blocked => "blocked",
        PaneStatus::Done => "done",
    }
}

/// Translate a crossterm key event into the bytes a PTY-attached program
/// expects on stdin. Deliberately minimal — enough to drive a shell.
fn key_to_bytes(code: KeyCode, mods: KeyModifiers) -> Option<Vec<u8>> {
    if mods.contains(KeyModifiers::CONTROL)
        && let KeyCode::Char(c) = code {
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
        KeyCode::Home => Some(b"\x1b[H".to_vec()),
        KeyCode::End => Some(b"\x1b[F".to_vec()),
        _ => None,
    }
}

fn run(mut terminal: DefaultTerminal) -> io::Result<()> {
    let mut app = App::new()?;

    loop {
        app.reap_dead();
        if app.panes.is_empty() {
            break;
        }

        terminal.draw(|frame| app.draw(frame))?;

        if !event::poll(Duration::from_millis(33))? {
            continue;
        }

        match event::read()? {
            Event::Key(key) if key.kind == KeyEventKind::Press => {
                if app.awaiting_leader {
                    app.awaiting_leader = false;
                    match key.code {
                        KeyCode::Char('n') => app.next_pane(),
                        KeyCode::Char('p') => app.prev_pane(),
                        KeyCode::Char('t') => app.new_pane(),
                        KeyCode::Char('x') => app.kill_focused(),
                        KeyCode::Char('c') => {
                            if let Some(p) = app.panes.get_mut(app.focused) {
                                p.status = p.status.next();
                            }
                        }
                        KeyCode::Char('q') => break,
                        _ => {}
                    }
                    continue;
                }

                if key.modifiers.contains(KeyModifiers::CONTROL) && key.code == KeyCode::Char('b') {
                    app.awaiting_leader = true;
                    continue;
                }

                if let Some(bytes) = key_to_bytes(key.code, key.modifiers)
                    && let Some(pane) = app.panes.get_mut(app.focused) {
                        pane.write_bytes(&bytes);
                    }
            }
            Event::Paste(text) => {
                if let Some(pane) = app.panes.get_mut(app.focused) {
                    pane.write_bytes(text.as_bytes());
                }
            }
            _ => {}
        }
    }

    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Proves the plumbing end-to-end without a real TTY: spawn a pane,
    /// type a command into its PTY, and confirm vt100 parsed the shell's
    /// echoed output into the screen grid. This is the piece a full-screen
    /// interactive run can't be verified non-interactively, so it's the
    /// one thing worth automating here.
    #[test]
    fn spawn_write_and_parse_roundtrip() {
        let mut pane = spawn_pane("test", 24, 80).expect("spawn pane");
        pane.write_bytes(b"echo hello_mux_prototype\n");

        let mut found = false;
        for _ in 0..50 {
            std::thread::sleep(Duration::from_millis(100));
            let parser = pane.parser.lock().unwrap();
            if screen_contains(&parser, "hello_mux_prototype") {
                found = true;
                break;
            }
        }
        assert!(found, "expected pane output to contain the echoed marker");
    }

    fn screen_contains(parser: &vt100::Parser, needle: &str) -> bool {
        let screen = parser.screen();
        let (rows, cols) = screen.size();
        for row in 0..rows {
            let mut line = String::new();
            for col in 0..cols {
                if let Some(cell) = screen.cell(row, col) {
                    let contents = cell.contents();
                    if contents.is_empty() {
                        line.push(' ');
                    } else {
                        line.push_str(&contents);
                    }
                }
            }
            if line.contains(needle) {
                return true;
            }
        }
        false
    }
}

fn main() -> io::Result<()> {
    enable_raw_mode()?;
    let mut stdout = io::stdout();
    execute!(stdout, EnterAlternateScreen, EnableBracketedPaste)?;

    let terminal = ratatui::init();
    let result = run(terminal);

    ratatui::restore();
    execute!(io::stdout(), LeaveAlternateScreen, DisableBracketedPaste)?;
    disable_raw_mode()?;

    result
}
