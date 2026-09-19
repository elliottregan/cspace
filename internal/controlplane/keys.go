package controlplane

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"

	"github.com/elliottregan/cspace/internal/control"
)

// The dashboard's action names. These strings are the keys of the `tui.keys`
// object in lib/defaults.json and in a person's ~/.cspace/config.json, so
// renaming one is a breaking config change.
const (
	ActionMoveUp         = "moveUp"
	ActionMoveDown       = "moveDown"
	ActionAttach         = "attach"
	ActionSend           = "send"
	ActionInterrupt      = "interrupt"
	ActionTeardown       = "teardown"
	ActionBrowserRestart = "browserRestart"
	ActionBoot           = "boot"
	ActionRefresh        = "refresh"
	ActionHelp           = "help"
	ActionQuit           = "quit"
	ActionLeader         = "leader"

	// The pane actions. The first two are sidebar keys the design reserved
	// in step 3 and step 4 now binds; the rest are the leader's second keys,
	// declared as ordinary actions so one config mechanism covers every
	// binding in the program.
	ActionShell        = "shell"
	ActionSupervisor   = "supervisor"
	ActionFocusMain    = "focusMain"
	ActionFocusSidebar = "focusSidebar"
	ActionNextTab      = "nextTab"
	ActionPrevTab      = "prevTab"
	ActionNewPane      = "newPane"
	ActionClosePane    = "closePane"
	ActionScroll       = "scroll"
	ActionLive         = "live"
	ActionPasteImage   = "pasteImage"
)

// defaultKeys is the built-in keystroke list per action, and must stay
// identical to lib/defaults.json's tui.keys (keys_test.go locks the two
// together). Go carries it as well as the JSON so a binary whose embedded
// assets are missing an action still has a working dashboard.
//
// The leader's second keys are ordinary actions here rather than a nested
// map: one mechanism covers every binding, `tui.keys` stays flat, and
// key.Matches works the same in both contexts. `?` and `q` are deliberately
// shared between the sidebar and the leader — the design lists them under
// both, and one binding means one label in both footers.
var defaultKeys = map[string][]string{
	ActionMoveUp:         {"up", "k"},
	ActionMoveDown:       {"down", "j"},
	ActionAttach:         {"enter"},
	ActionShell:          {"s"},
	ActionSupervisor:     {"a"},
	ActionSend:           {"m"},
	ActionInterrupt:      {"i"},
	ActionTeardown:       {"d"},
	ActionBrowserRestart: {"b"},
	ActionBoot:           {"u"},
	ActionRefresh:        {"r"},
	ActionHelp:           {"?"},
	ActionQuit:           {"q"},
	ActionLeader:         {"ctrl+space"},
	ActionFocusMain:      {"tab"},
	ActionFocusSidebar:   {"h"},
	ActionNextTab:        {"n"},
	ActionPrevTab:        {"p"},
	ActionNewPane:        {"t"},
	ActionClosePane:      {"x"},
	ActionScroll:         {"["},
	ActionLive:           {"g"},
	ActionPasteImage:     {"v"},
}

// actionHelp is the label and description each binding shows in the footer
// and the help overlay. The label is hand-written ("↑/k" rather than
// "up/k") because it describes the DEFAULT keys; an action whose keys are
// overridden gets its label derived from those keys instead, by helpLabel —
// a footer that says "enter attach" when enter does nothing is worse than a
// less pretty label.
var actionHelp = map[string][2]string{
	ActionMoveUp:         {"↑/k", "up"},
	ActionMoveDown:       {"↓/j", "down"},
	ActionAttach:         {"enter", "attach"},
	ActionSend:           {"m", "send a turn"},
	ActionInterrupt:      {"i", "interrupt"},
	ActionTeardown:       {"d", "tear down"},
	ActionBrowserRestart: {"b", "restart browser"},
	ActionBoot:           {"u", "boot"},
	ActionRefresh:        {"r", "refresh"},
	ActionHelp:           {"?", "help"},
	ActionQuit:           {"q", "quit"},
	ActionShell:          {"s", "shell pane"},
	ActionSupervisor:     {"a", "supervisor"},
	ActionFocusMain:      {"tab", "focus pane"},
	ActionFocusSidebar:   {"h", "sidebar"},
	ActionNextTab:        {"n", "next tab"},
	ActionPrevTab:        {"p", "prev tab"},
	ActionNewPane:        {"t", "new"},
	ActionClosePane:      {"x", "close"},
	ActionScroll:         {"[", "scroll"},
	ActionLive:           {"g", "live"},
	ActionPasteImage:     {"v", "paste image"},
}

// forceQuit is Ctrl+C: always bound, never configurable, and handled before
// anything else sees a key. A dashboard that could be configured into having
// no way out is a bug, and Ctrl+C has to work while a modal or the send box
// holds every other key.
var forceQuit = key.NewBinding(key.WithKeys("ctrl+c"))

// KeyMap is the dashboard's bindings. A KeyMap is a value: forRow returns a
// copy with the bindings the selection cannot use disabled, and both
// key.Matches and bubbles/help skip disabled bindings — so one filtered copy
// drives the gating and the footer at once.
type KeyMap struct {
	MoveUp         key.Binding
	MoveDown       key.Binding
	Attach         key.Binding
	Send           key.Binding
	Interrupt      key.Binding
	Teardown       key.Binding
	BrowserRestart key.Binding
	Boot           key.Binding
	Refresh        key.Binding
	Help           key.Binding
	Quit           key.Binding

	// Leader is the prefix for every pane binding. Ctrl+Space by default,
	// and it must not be Ctrl+B — Claude Code uses that to background a
	// task, and the whole point of a pane is that Claude's keys reach it.
	Leader key.Binding

	Shell        key.Binding
	Supervisor   key.Binding
	FocusMain    key.Binding
	FocusSidebar key.Binding
	NextTab      key.Binding
	PrevTab      key.Binding
	NewPane      key.Binding
	ClosePane    key.Binding
	Scroll       key.Binding
	Live         key.Binding
	PasteImage   key.Binding
}

// NewKeyMap builds the bindings, applying the user-level config's tui.keys
// over the built-in defaults. An action missing from overrides — or present
// with an empty list, which is what a config round-tripped through some
// other tool can produce — keeps its default. An unknown action name is
// ignored rather than rejected: a config written for a newer cspace must not
// stop an older one from starting.
func NewKeyMap(overrides map[string][]string) KeyMap {
	binding := func(action string) key.Binding {
		keys := defaultKeys[action]
		if over, ok := overrides[action]; ok && len(over) > 0 {
			keys = over
		}
		opts := []key.BindingOpt{key.WithKeys(keys...)}
		if h, ok := actionHelp[action]; ok {
			opts = append(opts, key.WithHelp(helpLabel(action, keys, h[0]), h[1]))
		}
		return key.NewBinding(opts...)
	}
	return KeyMap{
		MoveUp:         binding(ActionMoveUp),
		MoveDown:       binding(ActionMoveDown),
		Attach:         binding(ActionAttach),
		Send:           binding(ActionSend),
		Interrupt:      binding(ActionInterrupt),
		Teardown:       binding(ActionTeardown),
		BrowserRestart: binding(ActionBrowserRestart),
		Boot:           binding(ActionBoot),
		Refresh:        binding(ActionRefresh),
		Help:           binding(ActionHelp),
		Quit:           binding(ActionQuit),
		Leader:         binding(ActionLeader),
		Shell:          binding(ActionShell),
		Supervisor:     binding(ActionSupervisor),
		FocusMain:      binding(ActionFocusMain),
		FocusSidebar:   binding(ActionFocusSidebar),
		NextTab:        binding(ActionNextTab),
		PrevTab:        binding(ActionPrevTab),
		NewPane:        binding(ActionNewPane),
		ClosePane:      binding(ActionClosePane),
		Scroll:         binding(ActionScroll),
		Live:           binding(ActionLive),
		PasteImage:     binding(ActionPasteImage),
	}
}

// helpLabel is the key label the footer and the help overlay show for one
// action: the curated default when the action still has its default keys,
// and the configured keys themselves when it does not.
//
// Deriving it always would turn "↑/k up" into "up/k up"; never deriving it
// leaves the footer advertising a key the config has unbound, which is what
// `{"tui":{"keys":{"attach":["o"]}}}` used to produce — a footer reading
// "enter attach" beside an enter that did nothing.
func helpLabel(action string, keys []string, fallback string) string {
	if slices.Equal(keys, defaultKeys[action]) {
		return fallback
	}
	return strings.Join(keys, "/")
}

// ShortHelp is the footer's one line, in the order a person reads it.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.MoveUp, k.MoveDown, k.Attach, k.Send, k.Interrupt,
		k.Teardown, k.BrowserRestart, k.Boot, k.Help, k.Quit}
}

// FullHelp is the help overlay, grouped into columns: moving, acting on the
// selection, and the dashboard itself.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.MoveUp, k.MoveDown},
		{k.Attach, k.Send, k.Interrupt},
		{k.Teardown, k.Boot, k.BrowserRestart},
		{k.Refresh, k.Help, k.Quit},
	}
}

// LeaderHelp is the footer the main area gets: what the leader's second keys
// do, in the design's order. PasteImage is listed because it is bound and
// the config shape is stable; rollout step 5 is what makes it act.
//
// It is deliberately shorter than the full set of second keys. This is ONE
// line shared with the leader's own label, and help.ShortHelpView elides
// from the right once it runs out of width — so a list that does not fit is
// a list whose tail nobody ever reads. All nine render to about 98 cells,
// which overflows the design's 100-column reference window before the
// leader's seven-cell prefix is even counted; these seven fit in 76. The two
// that give are the ones already advertised elsewhere: PrevTab is in
// PaneFullHelp below, and Help is in FullHelp, which the overlay renders
// first.
func (k KeyMap) LeaderHelp() []key.Binding {
	return []key.Binding{k.FocusSidebar, k.NextTab, k.NewPane,
		k.ClosePane, k.Scroll, k.PasteImage, k.Quit}
}

// PaneFullHelp is the help overlay's second block: the sidebar keys that
// open a pane, and the leader's second keys.
//
// It is a separate method rather than three more columns on FullHelp
// because help.FullHelpView drops whole columns once their total exceeds
// its width and appends an ellipsis. The overlay renders into the main
// area — 74 columns at a 100-column window — and FullHelp's four columns
// already fill that, so a fifth and sixth would never be drawn at any
// realistic size. helpView renders this as its own row instead, which
// gives it the width back.
func (k KeyMap) PaneFullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Shell, k.Supervisor, k.FocusMain},
		{k.FocusSidebar, k.NextTab, k.PrevTab},
		{k.NewPane, k.ClosePane, k.Scroll, k.Live},
	}
}

// forRow returns a copy with every binding the selection cannot act on
// disabled. Disabling rather than branching at dispatch time is what keeps
// the footer and the gate from ever disagreeing: key.Matches ignores a
// disabled binding, and help.ShortHelpView skips it.
func (k KeyMap) forRow(row control.Row, live liveState) KeyMap {
	k.Attach.SetEnabled(canAttach(row))
	k.Teardown.SetEnabled(canDown(row))
	k.Boot.SetEnabled(canBoot(row))
	k.Send.SetEnabled(canSend(row, live))
	k.Interrupt.SetEnabled(canInterrupt(row, live))
	k.BrowserRestart.SetEnabled(canBrowser(row))
	k.Shell.SetEnabled(canShell(row))
	k.Supervisor.SetEnabled(canSupervisor(row))
	return k
}

// The contextual predicates. Pure, and tested directly: a running container
// is any State other than StateStopped.

func canAttach(r control.Row) bool {
	return r.Kind == control.RowSandbox && r.State != control.StateStopped
}

func canDown(r control.Row) bool {
	return r.Kind == control.RowSandbox && r.State != control.StateStopped
}

// canBoot is the mirror of canDown: `u` offers to start what is registered
// but not running. A sandbox that is already up has nothing to boot.
func canBoot(r control.Row) bool {
	return r.Kind == control.RowSandbox && r.State == control.StateStopped
}

func canSend(r control.Row, l liveState) bool {
	return r.Kind == control.RowSandbox && agentOf(r, l).Reachable
}

func canInterrupt(r control.Row, l liveState) bool {
	a := agentOf(r, l)
	return r.Kind == control.RowSandbox && a.Reachable && a.State == "working"
}

func canBrowser(r control.Row) bool {
	return r.Kind == control.RowBrowser || r.Kind == control.RowSandbox
}

// canShell mirrors canAttach: a shell pane runs inside the container, so
// there has to be one.
func canShell(r control.Row) bool {
	return r.Kind == control.RowSandbox && r.State != control.StateStopped
}

// canSupervisor is wider than the other two on purpose. The supervisor view
// reads events.ndjson from the host's session directory, which survives the
// container — so a sandbox stopped with `cspace down --keep-state` still has
// a readable history, and reading it is often exactly what a person wants
// before deciding whether to boot it again.
func canSupervisor(r control.Row) bool { return r.Kind == control.RowSandbox }

// agentOf prefers the fast ticker's fresh status and falls back to the one
// the snapshot carried, so the first second after start — before any fast
// tick has landed — does not read as "supervisor unreachable" and disable
// send and interrupt on every row.
func agentOf(r control.Row, l liveState) control.AgentStatus {
	if l.Agent.Reachable {
		return l.Agent
	}
	return r.Agent
}
