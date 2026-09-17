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
//!   Ctrl+b then x       kill/dismiss focused pane
//!   Ctrl+b then c       cycle focused pane's status override
//!                       (none → blocked → done → none); "working"/"idle"
//!                       are derived automatically from real PTY output
//!                       activity, not toggled — see `effective_status`.
//!   Ctrl+b then u / d   scroll the focused pane's scrollback up / down
//!   Ctrl+b then g       jump the focused pane back to live output
//!   Ctrl+b then m       toggle single-pane / grid (all panes at once)
//!   Ctrl+b then q       quit
//!   anything else       forwarded to the focused pane's shell

use std::io::{self, Read, Write};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

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
use ratatui::widgets::{Block, Borders, List, ListItem, ListState, Paragraph, Tabs};
use ratatui::{DefaultTerminal, Frame};
use tui_term::widget::PseudoTerminal;

/// How many scrollback lines each pane's vt100 parser retains.
const SCROLLBACK_LINES: usize = 10_000;
/// How many lines a single leader+u/d scroll step moves.
const SCROLL_STEP: usize = 10;
/// A pane counts as "working" if it produced output more recently than this.
const IDLE_THRESHOLD: Duration = Duration::from_secs(2);
/// Fallback pane size before the first real terminal size is known.
const DEFAULT_PANE_SIZE: (u16, u16) = (24, 80);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum PaneStatus {
    Working,
    Idle,
    Blocked,
    Done,
    Exited,
}

impl PaneStatus {
    fn glyph(self) -> &'static str {
        match self {
            PaneStatus::Working => "●",
            PaneStatus::Idle => "○",
            PaneStatus::Blocked => "▲",
            PaneStatus::Done => "✓",
            PaneStatus::Exited => "✕",
        }
    }

    fn color(self) -> Color {
        match self {
            PaneStatus::Working => Color::Green,
            PaneStatus::Idle => Color::DarkGray,
            PaneStatus::Blocked => Color::Yellow,
            PaneStatus::Done => Color::Blue,
            PaneStatus::Exited => Color::Red,
        }
    }

    fn label(self) -> &'static str {
        match self {
            PaneStatus::Working => "working",
            PaneStatus::Idle => "idle",
            PaneStatus::Blocked => "blocked",
            PaneStatus::Done => "done",
            PaneStatus::Exited => "exited",
        }
    }
}

/// Manual status override cycle, independent of the automatic
/// working/idle signal derived from real output activity.
#[derive(Clone, Copy, PartialEq, Eq)]
enum StatusOverride {
    None,
    Blocked,
    Done,
}

impl StatusOverride {
    fn next(self) -> Self {
        match self {
            StatusOverride::None => StatusOverride::Blocked,
            StatusOverride::Blocked => StatusOverride::Done,
            StatusOverride::Done => StatusOverride::None,
        }
    }
}

/// One pane: owns the child's PTY master + writer, and the vt100 parser
/// that a background thread feeds from the PTY's byte stream. tui-term
/// only ever sees `parser.screen()` at render time — it never touches
/// the raw bytes itself.
struct Pane {
    title: String,
    status_override: StatusOverride,
    /// Set by the reader thread on every non-empty read; read by the UI
    /// thread to derive a real (not simulated) working/idle signal.
    last_activity: Arc<Mutex<Instant>>,
    /// Set by the reader thread when it hits EOF (child exited). The pane
    /// stays visible with an "exited" status until explicitly dismissed
    /// (Ctrl+b x) rather than vanishing the moment the process dies.
    exited: Arc<AtomicBool>,
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
    let child = pair.slave.spawn_command(cmd).map_err(io::Error::other)?;
    // The slave end belongs to the child now; drop our copy so the
    // master's reader sees EOF when the child actually exits.
    drop(pair.slave);

    let reader = pair
        .master
        .try_clone_reader()
        .map_err(io::Error::other)?;
    let writer = pair.master.take_writer().map_err(io::Error::other)?;

    let parser = Arc::new(Mutex::new(vt100::Parser::new(rows, cols, SCROLLBACK_LINES)));
    let last_activity = Arc::new(Mutex::new(Instant::now()));
    let exited = Arc::new(AtomicBool::new(false));

    let reader_parser = Arc::clone(&parser);
    let reader_activity = Arc::clone(&last_activity);
    let reader_exited = Arc::clone(&exited);
    std::thread::spawn(move || {
        let mut reader = reader;
        let mut buf = [0u8; 8192];
        loop {
            match reader.read(&mut buf) {
                Ok(0) => break, // child exited
                Ok(n) => {
                    reader_parser.lock().unwrap().process(&buf[..n]);
                    *reader_activity.lock().unwrap() = Instant::now();
                }
                Err(_) => break,
            }
        }
        reader_exited.store(true, Ordering::Relaxed);
    });

    Ok(Pane {
        title: title.into(),
        status_override: StatusOverride::None,
        last_activity,
        exited,
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
        if self.exited.load(Ordering::Relaxed) {
            return;
        }
        let _ = self.writer.write_all(bytes);
    }

    /// True once the reader thread has observed EOF. This is the
    /// authoritative "did the process die" signal; `Child::try_wait`
    /// alone can race the reader thread draining the last bytes.
    fn has_exited(&self) -> bool {
        self.exited.load(Ordering::Relaxed)
    }

    fn scroll(&mut self, delta: isize) {
        let mut parser = self.parser.lock().unwrap();
        let current = parser.screen().scrollback();
        let next = if delta.is_negative() {
            current.saturating_sub(delta.unsigned_abs())
        } else {
            current.saturating_add(delta as usize)
        };
        parser.screen_mut().set_scrollback(next);
    }

    fn jump_to_live(&mut self) {
        self.parser.lock().unwrap().screen_mut().set_scrollback(0);
    }

    /// The status actually shown: an exited pane always shows as exited;
    /// otherwise a manual override (blocked/done) sticks, and absent
    /// that, working/idle is derived from real recent output activity.
    fn effective_status(&self) -> PaneStatus {
        if self.has_exited() {
            return PaneStatus::Exited;
        }
        match self.status_override {
            StatusOverride::Blocked => PaneStatus::Blocked,
            StatusOverride::Done => PaneStatus::Done,
            StatusOverride::None => {
                let idle_for = self.last_activity.lock().unwrap().elapsed();
                if idle_for < IDLE_THRESHOLD {
                    PaneStatus::Working
                } else {
                    PaneStatus::Idle
                }
            }
        }
    }
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum LayoutMode {
    /// The focused pane fills the whole content area; the mechanism
    /// proven first (a single `PseudoTerminal` into a sub-`Rect`).
    Single,
    /// Every pane renders simultaneously in an auto-computed grid. Each
    /// visible pane is resized to its own cell, not a shared size.
    Grid,
}

struct App {
    panes: Vec<Pane>,
    focused: usize,
    awaiting_leader: bool,
    next_id: usize,
    layout_mode: LayoutMode,
    /// Last computed (rows, cols) available for pane content, kept in
    /// sync by resize_all_panes so newly spawned panes start at the
    /// right size instead of a hardcoded default.
    content_size: (u16, u16),
    last_error: Option<String>,
}

impl App {
    fn new() -> io::Result<Self> {
        let first = spawn_pane("sandbox-1", DEFAULT_PANE_SIZE.0, DEFAULT_PANE_SIZE.1)?;
        Ok(Self {
            panes: vec![first],
            focused: 0,
            awaiting_leader: false,
            next_id: 2,
            layout_mode: LayoutMode::Single,
            content_size: DEFAULT_PANE_SIZE,
            last_error: None,
        })
    }

    fn new_pane(&mut self) {
        let (rows, cols) = self.content_size;
        match spawn_pane(format!("sandbox-{}", self.next_id), rows, cols) {
            Ok(p) => {
                self.next_id += 1;
                self.panes.push(p);
                self.focused = self.panes.len() - 1;
                self.last_error = None;
            }
            Err(e) => {
                self.last_error = Some(format!("spawn failed: {e}"));
            }
        }
    }

    fn kill_focused(&mut self) {
        if self.panes.len() <= 1 {
            return; // keep at least one pane in this prototype
        }
        let mut pane = self.panes.remove(self.focused);
        // Explicit kill rather than relying on Drop: an exited pane's
        // child is already gone, so this only matters for dismissing a
        // still-running one, but it's the honest way to end it either way.
        let _ = pane.child.kill();
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

    /// Resizes every pane (not just the focused one) so switching focus
    /// after a window resize never shows stale-size content.
    fn resize_all_panes(&mut self, rows: u16, cols: u16) {
        self.content_size = (rows, cols);
        for pane in &mut self.panes {
            pane.resize(rows, cols);
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
            .constraints([Constraint::Length(3), Constraint::Min(1), Constraint::Length(1)])
            .split(root[1]);

        self.draw_tabs(frame, main[0]);
        match self.layout_mode {
            LayoutMode::Single => self.draw_focused_pane(frame, main[1]),
            LayoutMode::Grid => self.draw_grid_panes(frame, main[1]),
        }
        self.draw_footer(frame, main[2]);
    }

    fn draw_sidebar(&self, frame: &mut Frame, area: Rect) {
        let items: Vec<ListItem> = self
            .panes
            .iter()
            .map(|p| {
                let status = p.effective_status();
                let line = Line::from(vec![
                    Span::styled(format!("{} ", status.glyph()), Style::default().fg(status.color())),
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
            let status = pane.effective_status();
            let scroll_note = if screen.scrollback() > 0 {
                format!(" [scrollback -{}]", screen.scrollback())
            } else {
                String::new()
            };
            let title = format!(" {} — {}{} ", pane.title, status.label(), scroll_note);
            let widget = PseudoTerminal::new(screen)
                .block(Block::default().title(title).borders(Borders::ALL).border_style(Style::default().fg(status.color())));
            frame.render_widget(widget, area);
        }
    }

    /// Renders every pane at once in an auto-computed grid, each resized
    /// to its own cell (not the shared `content_size` single-pane logic
    /// uses). The focused pane's border is bolded so it's clear which one
    /// receives input; all panes are live simultaneously, not just shown.
    fn draw_grid_panes(&mut self, frame: &mut Frame, area: Rect) {
        let rects = grid_rects(area, self.panes.len());
        let focused = self.focused;
        for (i, (pane, rect)) in self.panes.iter_mut().zip(rects).enumerate() {
            let inner_rows = rect.height.saturating_sub(2);
            let inner_cols = rect.width.saturating_sub(2);
            pane.resize(inner_rows, inner_cols);

            let status = pane.effective_status();
            let parser = pane.parser.lock().unwrap();
            let screen = parser.screen();
            let title = format!(" {} — {} ", pane.title, status.label());
            let mut border_style = Style::default().fg(status.color());
            if i == focused {
                border_style = border_style.add_modifier(Modifier::BOLD);
            }
            let widget = PseudoTerminal::new(screen)
                .block(Block::default().title(title).borders(Borders::ALL).border_style(border_style));
            frame.render_widget(widget, rect);
        }
    }

    fn draw_footer(&self, frame: &mut Frame, area: Rect) {
        if let Some(err) = &self.last_error {
            let footer = Paragraph::new(Line::from(Span::styled(
                format!(" {err}"),
                Style::default().fg(Color::Red).add_modifier(Modifier::BOLD),
            )));
            frame.render_widget(footer, area);
        }
    }
}

/// Translate a crossterm key event into the bytes a PTY-attached program
/// expects on stdin. Deliberately minimal — enough to drive a shell.
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
        KeyCode::Home => Some(b"\x1b[H".to_vec()),
        KeyCode::End => Some(b"\x1b[F".to_vec()),
        _ => None,
    }
}

/// Computes the pane content rect for a given full-terminal size, mirroring
/// `App::draw`'s layout. Used outside the draw loop (on resize events) so
/// every pane — not just the focused one — can be resized immediately.
fn pane_content_rect(full: Rect) -> Rect {
    let root = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Length(24), Constraint::Min(1)])
        .split(full);
    let main = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(3), Constraint::Min(1), Constraint::Length(1)])
        .split(root[1]);
    main[1]
}

/// Computes a balanced (rows, cols) grid for `n` panes: `cols` is the
/// smallest value with `cols * cols >= n`, `rows` is however many are
/// then needed to fit `n` panes at that width. `(0, 0)` for `n == 0`.
fn grid_dims(n: usize) -> (usize, usize) {
    if n == 0 {
        return (0, 0);
    }
    let cols = (n as f64).sqrt().ceil() as usize;
    let rows = n.div_ceil(cols);
    (rows, cols)
}

/// Splits `area` into exactly `n` rects in row-major order. A partially
/// filled last row stretches across the full width among however many
/// panes actually land there, rather than leaving empty grid cells.
fn grid_rects(area: Rect, n: usize) -> Vec<Rect> {
    if n == 0 {
        return Vec::new();
    }
    let (rows, cols) = grid_dims(n);
    let row_constraints = vec![Constraint::Ratio(1, rows as u32); rows];
    let row_areas = Layout::default().direction(Direction::Vertical).constraints(row_constraints).split(area);

    let mut rects = Vec::with_capacity(n);
    let mut remaining = n;
    for row_area in row_areas.iter() {
        let cols_this_row = remaining.min(cols);
        if cols_this_row == 0 {
            break;
        }
        let col_constraints = vec![Constraint::Ratio(1, cols_this_row as u32); cols_this_row];
        let col_areas = Layout::default().direction(Direction::Horizontal).constraints(col_constraints).split(*row_area);
        rects.extend(col_areas.iter().copied());
        remaining -= cols_this_row;
    }
    rects
}

fn run(mut terminal: DefaultTerminal) -> io::Result<()> {
    let mut app = App::new()?;

    // Size every pane to the real terminal before the first draw, instead
    // of leaving them at DEFAULT_PANE_SIZE until something triggers a
    // resize.
    let size = terminal.size()?;
    let area = pane_content_rect(Rect::new(0, 0, size.width, size.height));
    app.resize_all_panes(area.height.saturating_sub(2), area.width.saturating_sub(2));

    loop {
        // Exited panes stay visible (marked via effective_status) until
        // the user explicitly dismisses them with Ctrl+b x; they are
        // never auto-removed here.
        if app.panes.is_empty() {
            break;
        }

        terminal.draw(|frame| app.draw(frame))?;

        if !event::poll(Duration::from_millis(33))? {
            continue;
        }

        match event::read()? {
            Event::Resize(width, height) => {
                let area = pane_content_rect(Rect::new(0, 0, width, height));
                app.resize_all_panes(area.height.saturating_sub(2), area.width.saturating_sub(2));
            }
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
                                p.status_override = p.status_override.next();
                            }
                        }
                        KeyCode::Char('u') => {
                            if let Some(p) = app.panes.get_mut(app.focused) {
                                p.scroll(SCROLL_STEP as isize);
                            }
                        }
                        KeyCode::Char('d') => {
                            if let Some(p) = app.panes.get_mut(app.focused) {
                                p.scroll(-(SCROLL_STEP as isize));
                            }
                        }
                        KeyCode::Char('g') => {
                            if let Some(p) = app.panes.get_mut(app.focused) {
                                p.jump_to_live();
                            }
                        }
                        KeyCode::Char('m') => {
                            app.layout_mode = match app.layout_mode {
                                LayoutMode::Single => LayoutMode::Grid,
                                LayoutMode::Grid => LayoutMode::Single,
                            };
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
                    && let Some(pane) = app.panes.get_mut(app.focused)
                {
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

    /// Covers the activity-derived status gap closed in this pass: a
    /// freshly-active pane reads as Working, and after it goes quiet
    /// past IDLE_THRESHOLD it reads as Idle without any manual toggle.
    #[test]
    fn effective_status_tracks_real_activity() {
        let pane = spawn_pane("activity-test", 24, 80).expect("spawn pane");
        assert_eq!(pane.effective_status(), PaneStatus::Working);

        *pane.last_activity.lock().unwrap() = Instant::now() - IDLE_THRESHOLD - Duration::from_millis(1);
        assert_eq!(pane.effective_status(), PaneStatus::Idle);
    }

    /// Covers the "exited pane vanishes silently" gap: a dead child's
    /// pane must report Exited via has_exited/effective_status, and that
    /// must win over any status override, instead of being reaped
    /// invisibly.
    #[test]
    fn exited_pane_is_visible_not_silently_removed() {
        let mut pane = spawn_pane("exit-test", 24, 80).expect("spawn pane");
        pane.write_bytes(b"exit\n");

        let mut observed_exit = false;
        for _ in 0..50 {
            std::thread::sleep(Duration::from_millis(100));
            if pane.has_exited() {
                observed_exit = true;
                break;
            }
        }
        assert!(observed_exit, "expected reader thread to observe EOF after shell exit");
        assert_eq!(pane.effective_status(), PaneStatus::Exited);

        pane.status_override = StatusOverride::Blocked;
        assert_eq!(
            pane.effective_status(),
            PaneStatus::Exited,
            "exited must win over a manual override"
        );
    }

    /// The multi-pane feature's actual risk was never "can ratatui render
    /// into a sub-Rect" (already proven by the single-pane path) — it's
    /// getting the grid math right: exactly n rects, none overlapping,
    /// all inside the given area, for a range of pane counts including
    /// the awkward ones (1, primes, a perfect square).
    #[test]
    fn grid_rects_covers_every_pane_without_overlap_or_overflow() {
        let area = Rect::new(0, 0, 120, 40);
        for n in [1usize, 2, 3, 4, 5, 7, 9, 16] {
            let rects = grid_rects(area, n);
            assert_eq!(rects.len(), n, "expected {n} rects for {n} panes");

            for r in &rects {
                assert!(
                    r.x >= area.x && r.y >= area.y && r.right() <= area.right() && r.bottom() <= area.bottom(),
                    "rect {r:?} escapes the {area:?} bounds for n={n}"
                );
            }

            for i in 0..rects.len() {
                for j in (i + 1)..rects.len() {
                    assert!(
                        !rects_overlap(rects[i], rects[j]),
                        "rects {i} and {j} overlap for n={n}: {:?} vs {:?}",
                        rects[i],
                        rects[j]
                    );
                }
            }
        }
    }

    #[test]
    fn grid_dims_is_at_least_as_big_as_pane_count() {
        for n in 1usize..=20 {
            let (rows, cols) = grid_dims(n);
            assert!(rows * cols >= n, "grid {rows}x{cols} too small for {n} panes");
        }
        assert_eq!(grid_dims(0), (0, 0));
    }

    fn rects_overlap(a: Rect, b: Rect) -> bool {
        a.x < b.right() && b.x < a.right() && a.y < b.bottom() && b.y < a.bottom()
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
                        line.push_str(contents);
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
