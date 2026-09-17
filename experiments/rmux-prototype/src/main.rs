//! Prototype #2: the same herdr-style dashboard as `mux-prototype`, but
//! backed by `rmux-sdk` + `ratatui-rmux` instead of hand-rolled
//! portable-pty + vt100 + tui-term.
//!
//! `rmux-sdk`'s `Rmux::builder().connect_or_start()` spawns/attaches to a
//! separate `rmux-daemon` process (found via `RMUX_SDK_DAEMON_BINARY`, or
//! `rmux-daemon`/`rmux` on PATH). Panes live in the daemon, not this
//! process — `PaneDriver::refresh()` just pulls a snapshot each tick.
//! Input goes through `Pane::send_text`/`send_key` (tmux-style tokens)
//! instead of hand-translated raw bytes.
//!
//! Controls: same as mux-prototype — Ctrl+b then n/p/t/x/c/q.
//!
//! Scope note: panes are spawned at a fixed 80x24 and never resized on
//! layout changes here (unlike mux-prototype's PTY resize) — `Pane::resize`
//! exists and is async, it's just not wired into this spike's sync draw
//! path. Not needed to prove the daemon round-trip.

use std::io;
use std::time::Duration;

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
use ratatui_rmux::{PaneDriver, PaneWidget};
use rmux_sdk::{EnsureSession, EnsureSessionPolicy, ProcessSpec, Result, Rmux, SessionName, TerminalSizeSpec};

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

struct PaneEntry {
    title: String,
    status: PaneStatus,
    driver: PaneDriver,
}

/// Ensures a named daemon-backed session exists and returns a driver for
/// its first pane. Each cspace "sandbox" maps to one rmux session here;
/// in the real integration this is where `container exec -it <sandbox>
/// claude` would replace the default shell process.
async fn spawn_pane(rmux: &Rmux, name: &str, rows: u16, cols: u16) -> Result<PaneEntry> {
    let session_name = SessionName::new(name).expect("valid session name");
    let session = rmux
        .ensure_session(
            EnsureSession::named(session_name)
                .policy(EnsureSessionPolicy::CreateOrReuse)
                .detached(true)
                .size(TerminalSizeSpec::new(cols, rows))
                .process(ProcessSpec::default()),
        )
        .await?;
    let pane = session.pane(0, 0);
    Ok(PaneEntry {
        title: name.to_string(),
        status: PaneStatus::Working,
        driver: PaneDriver::new(pane),
    })
}

struct App {
    rmux: Rmux,
    panes: Vec<PaneEntry>,
    focused: usize,
    awaiting_leader: bool,
    next_id: usize,
}

impl App {
    async fn new(rmux: Rmux) -> Result<Self> {
        let first = spawn_pane(&rmux, "sandbox-1", 24, 80).await?;
        Ok(Self {
            rmux,
            panes: vec![first],
            focused: 0,
            awaiting_leader: false,
            next_id: 2,
        })
    }

    async fn new_pane(&mut self) {
        let name = format!("sandbox-{}", self.next_id);
        if let Ok(p) = spawn_pane(&self.rmux, &name, 24, 80).await {
            self.next_id += 1;
            self.panes.push(p);
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

    fn kill_focused(&mut self) {
        if self.panes.len() <= 1 {
            return;
        }
        self.panes.remove(self.focused);
        if self.focused >= self.panes.len() {
            self.focused = self.panes.len() - 1;
        }
    }

    fn draw(&self, frame: &mut Frame) {
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
            .block(Block::default().title(" sandboxes (rmux) ").borders(Borders::ALL))
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

    fn draw_focused_pane(&self, frame: &mut Frame, area: Rect) {
        if let Some(pane) = self.panes.get(self.focused) {
            let title = format!(" {} — daemon-backed ", pane.title);
            let widget = PaneWidget::new(pane.driver.state())
                .base_style(Style::default());
            let block = Block::default().title(title).borders(Borders::ALL);
            let inner = block.inner(area);
            frame.render_widget(block, area);
            frame.render_widget(widget, inner);
        }
    }
}

/// Translates a crossterm key into the tmux-style token `send_key`
/// expects, or `None` when it should go through `send_text` instead.
fn key_to_token(code: KeyCode, mods: KeyModifiers) -> Option<String> {
    if mods.contains(KeyModifiers::CONTROL)
        && let KeyCode::Char(c) = code {
            return Some(format!("C-{}", c.to_ascii_lowercase()));
        }
    match code {
        KeyCode::Enter => Some("Enter".to_string()),
        KeyCode::Backspace => Some("BSpace".to_string()),
        KeyCode::Tab => Some("Tab".to_string()),
        KeyCode::Esc => Some("Escape".to_string()),
        KeyCode::Up => Some("Up".to_string()),
        KeyCode::Down => Some("Down".to_string()),
        KeyCode::Left => Some("Left".to_string()),
        KeyCode::Right => Some("Right".to_string()),
        KeyCode::Home => Some("Home".to_string()),
        KeyCode::End => Some("End".to_string()),
        _ => None,
    }
}

async fn run(mut terminal: DefaultTerminal, rmux: Rmux) -> Result<()> {
    let mut app = App::new(rmux).await?;

    loop {
        if app.panes.is_empty() {
            break;
        }

        if let Some(pane) = app.panes.get_mut(app.focused) {
            let _ = pane.driver.refresh().await;
        }

        terminal.draw(|frame| app.draw(frame)).ok();

        if !event::poll(Duration::from_millis(0)).unwrap_or(false) {
            tokio::time::sleep(Duration::from_millis(33)).await;
            continue;
        }

        match event::read() {
            Ok(Event::Key(key)) if key.kind == KeyEventKind::Press => {
                if app.awaiting_leader {
                    app.awaiting_leader = false;
                    match key.code {
                        KeyCode::Char('n') => app.next_pane(),
                        KeyCode::Char('p') => app.prev_pane(),
                        KeyCode::Char('t') => app.new_pane().await,
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

                let Some(pane) = app.panes.get(app.focused) else {
                    continue;
                };
                if let Some(token) = key_to_token(key.code, key.modifiers) {
                    let _ = pane.driver.pane().send_key(token).await;
                } else if let KeyCode::Char(c) = key.code {
                    let _ = pane.driver.pane().send_text(c.to_string()).await;
                }
            }
            Ok(Event::Paste(text)) => {
                if let Some(pane) = app.panes.get(app.focused) {
                    let _ = pane.driver.pane().send_text(text).await;
                }
            }
            _ => {}
        }
    }

    Ok(())
}

#[tokio::main]
async fn main() -> io::Result<()> {
    let rmux = Rmux::builder()
        .default_timeout(Duration::from_secs(5))
        .connect_or_start()
        .await
        .map_err(io::Error::other)?;

    enable_raw_mode()?;
    execute!(io::stdout(), EnterAlternateScreen, EnableBracketedPaste)?;
    let terminal = ratatui::init();

    let result = run(terminal, rmux).await;

    ratatui::restore();
    execute!(io::stdout(), LeaveAlternateScreen, DisableBracketedPaste)?;
    disable_raw_mode()?;

    result.map_err(io::Error::other)
}

#[cfg(test)]
mod tests {
    use super::*;
    use ratatui_rmux::theme::glyph_symbol;

    /// Same proof as mux-prototype's smoke test, one layer up: connect to
    /// a real (auto-started) rmux daemon, spawn a session/pane, type into
    /// it, and confirm the daemon-captured snapshot shows the echoed text.
    #[tokio::test]
    async fn spawn_write_and_snapshot_roundtrip() {
        let rmux = Rmux::builder()
            .default_timeout(Duration::from_secs(10))
            .connect_or_start()
            .await
            .expect("connect or start rmux daemon");

        let mut entry = spawn_pane(&rmux, "test-roundtrip", 24, 80)
            .await
            .expect("spawn pane");

        entry
            .driver
            .pane()
            .send_text("echo hello_rmux_prototype")
            .await
            .expect("send text");
        entry
            .driver
            .pane()
            .send_key("Enter")
            .await
            .expect("send enter");

        let mut found = false;
        for _ in 0..50 {
            tokio::time::sleep(Duration::from_millis(100)).await;
            entry.driver.refresh().await.expect("refresh snapshot");
            if snapshot_contains(&entry, "hello_rmux_prototype") {
                found = true;
                break;
            }
        }
        assert!(found, "expected pane output to contain the echoed marker");
    }

    fn snapshot_contains(entry: &PaneEntry, needle: &str) -> bool {
        let snapshot = &entry.driver.state().snapshot;
        for row in 0..snapshot.rows {
            let Some(cells) = snapshot.row_cells(row) else {
                continue;
            };
            let line: String = cells.iter().map(|cell| glyph_symbol(&cell.glyph)).collect();
            if line.contains(needle) {
                return true;
            }
        }
        false
    }
}
