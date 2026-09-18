package controlplane

import (
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
)

// defaultKeys is the built-in keystroke list per action, and must stay
// identical to lib/defaults.json's tui.keys (keys_test.go locks the two
// together). Go carries it as well as the JSON so a binary whose embedded
// assets are missing an action still has a working dashboard.
//
// `s` and `a` are the spec's sidebar keys for the shell pane and the
// supervisor view, which arrive with panes in rollout step 4. Neither is
// bound here — a default this ships and step 4 has to take back is a
// user-visible breaking change, and these strings are published in
// lib/defaults.json. So attach is Enter alone, and send is on `m` (for
// message) rather than on the `s` that is spoken for.
var defaultKeys = map[string][]string{
	ActionMoveUp:         {"up", "k"},
	ActionMoveDown:       {"down", "j"},
	ActionAttach:         {"enter"},
	ActionSend:           {"m"},
	ActionInterrupt:      {"i"},
	ActionTeardown:       {"d"},
	ActionBrowserRestart: {"b"},
	ActionBoot:           {"u"},
	ActionRefresh:        {"r"},
	ActionHelp:           {"?"},
	ActionQuit:           {"q"},
	ActionLeader:         {"ctrl+space"},
}

// actionHelp is the label and description each binding shows in the footer
// and the help overlay. The label is written for a human ("↑/k"), not
// derived from the keystrokes, so an overridden binding still reads well.
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

	// Leader is declared for rollout step 4's pane bindings so the config
	// shape is stable now. Nothing in step 3 dispatches it, and it is kept
	// out of ShortHelp/FullHelp so the footer does not advertise a key that
	// does nothing yet.
	Leader key.Binding
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
			opts = append(opts, key.WithHelp(h[0], h[1]))
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
	}
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
