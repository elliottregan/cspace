package controlplane

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"charm.land/bubbles/v2/key"

	"github.com/elliottregan/cspace/internal/assets"
	"github.com/elliottregan/cspace/internal/control"
)

func bindingFor(k KeyMap, action string) key.Binding {
	switch action {
	case ActionMoveUp:
		return k.MoveUp
	case ActionMoveDown:
		return k.MoveDown
	case ActionAttach:
		return k.Attach
	case ActionSend:
		return k.Send
	case ActionInterrupt:
		return k.Interrupt
	case ActionTeardown:
		return k.Teardown
	case ActionBrowserRestart:
		return k.BrowserRestart
	case ActionBoot:
		return k.Boot
	case ActionRefresh:
		return k.Refresh
	case ActionHelp:
		return k.Help
	case ActionQuit:
		return k.Quit
	case ActionLeader:
		return k.Leader
	}
	return key.Binding{}
}

func TestNewKeyMapUsesTheBuiltInDefaults(t *testing.T) {
	k := NewKeyMap(nil)
	for action, want := range defaultKeys {
		if got := bindingFor(k, action).Keys(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s keys = %v, want %v", action, got, want)
		}
		if bindingFor(k, action).Help().Desc == "" && action != ActionLeader {
			t.Errorf("%s has no help description", action)
		}
	}
}

func TestNewKeyMapAppliesOverrides(t *testing.T) {
	k := NewKeyMap(map[string][]string{
		ActionAttach: {"o"},
		"nonsense":   {"z"}, // unknown names are ignored, not fatal
	})
	if got := k.Attach.Keys(); !reflect.DeepEqual(got, []string{"o"}) {
		t.Errorf("attach keys = %v, want [o]", got)
	}
	if got := k.Quit.Keys(); !reflect.DeepEqual(got, defaultKeys[ActionQuit]) {
		t.Errorf("quit keys = %v, want the default %v", got, defaultKeys[ActionQuit])
	}
	// An empty list means "say nothing", not "unbind": a config that
	// round-trips through a tool emitting [] must not silently disarm a key.
	if got := NewKeyMap(map[string][]string{ActionAttach: {}}).Attach.Keys(); !reflect.DeepEqual(got, defaultKeys[ActionAttach]) {
		t.Errorf("empty override = %v, want the default", got)
	}
}

// defaults.json documents the shipped bindings and Go carries the fallback.
// They must not drift: a user who edits one action in ~/.cspace/config.json
// keeps defaults.json's values for the rest, and a binary whose embedded
// defaults disagreed with its own fallback would behave differently
// depending on which path a key came from.
func TestDefaultsJSONMatchesTheBuiltInKeys(t *testing.T) {
	raw, err := assets.DefaultsJSON()
	if err != nil {
		t.Fatalf("DefaultsJSON: %v", err)
	}
	var doc struct {
		TUI struct {
			Keys map[string][]string `json:"keys"`
		} `json:"tui"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse defaults.json: %v", err)
	}
	if !reflect.DeepEqual(doc.TUI.Keys, defaultKeys) {
		t.Errorf("defaults.json tui.keys = %v, want %v", doc.TUI.Keys, defaultKeys)
	}
	names := make([]string, 0, len(defaultKeys))
	for n := range defaultKeys {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if len(bindingFor(NewKeyMap(nil), n).Keys()) == 0 {
			t.Errorf("action %q resolves to no keys", n)
		}
	}
}

func TestForRowDisablesWhatTheSelectionCannotDo(t *testing.T) {
	working := control.Row{Kind: control.RowSandbox, State: control.StateRunning,
		Agent: control.AgentStatus{Reachable: true, State: "working"}}
	idle := control.Row{Kind: control.RowSandbox, State: control.StateRunning,
		Agent: control.AgentStatus{Reachable: true, State: "idle"}}
	stopped := control.Row{Kind: control.RowSandbox, State: control.StateStopped}
	degraded := control.Row{Kind: control.RowSandbox, State: control.StateDegraded}
	browser := control.Row{Kind: control.RowBrowser, State: control.StateRunning}

	cases := []struct {
		name   string
		row    control.Row
		live   liveState
		action string
		want   bool
	}{
		{"attach a running sandbox", working, liveState{}, ActionAttach, true},
		{"attach a stopped sandbox", stopped, liveState{}, ActionAttach, false},
		{"attach the browser row", browser, liveState{}, ActionAttach, false},
		{"boot a stopped sandbox", stopped, liveState{}, ActionBoot, true},
		{"boot a running sandbox", working, liveState{}, ActionBoot, false},
		{"down a running sandbox", working, liveState{}, ActionTeardown, true},
		{"down a stopped sandbox", stopped, liveState{}, ActionTeardown, false},
		{"send to a reachable agent", idle, liveState{}, ActionSend, true},
		{"send to a degraded sandbox", degraded, liveState{}, ActionSend, false},
		{"interrupt a working agent", working, liveState{}, ActionInterrupt, true},
		{"interrupt an idle agent", idle, liveState{}, ActionInterrupt, false},
		{"browser restart on a sandbox", working, liveState{}, ActionBrowserRestart, true},
		{"browser restart on the browser row", browser, liveState{}, ActionBrowserRestart, true},
		// The fast ticker's fresher status wins over the snapshot's: a
		// sandbox the snapshot saw idle but that is working now can be
		// interrupted without waiting for the next snapshot.
		{"interrupt on the live sample", idle,
			liveState{Agent: control.AgentStatus{Reachable: true, State: "working"}}, ActionInterrupt, true},
		// Keys that never depend on the selection stay enabled.
		{"move stays enabled", stopped, liveState{}, ActionMoveDown, true},
		{"help stays enabled", stopped, liveState{}, ActionHelp, true},
		{"quit stays enabled", stopped, liveState{}, ActionQuit, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bindingFor(NewKeyMap(nil).forRow(tc.row, tc.live), tc.action).Enabled()
			if got != tc.want {
				t.Errorf("%s enabled = %v, want %v", tc.action, got, tc.want)
			}
		})
	}
}

// forceQuit is the one binding no config can touch: NewKeyMap never sees it
// and Update matches it before any mode does, so a dashboard cannot be
// configured into having no way out.
func TestForceQuitIsCtrlCAndNotConfigurable(t *testing.T) {
	if got := forceQuit.Keys(); !reflect.DeepEqual(got, []string{"ctrl+c"}) {
		t.Errorf("forceQuit keys = %v, want [ctrl+c]", got)
	}
	if _, ok := defaultKeys["forceQuit"]; ok {
		t.Error("forceQuit must not be reachable from tui.keys")
	}
}

// keyOf is how per-sandbox state (agent status, interactive state, ports)
// survives a poll that rebuilds every row from scratch.
func TestKeyOfIdentifiesTheSandbox(t *testing.T) {
	row := control.Row{Kind: control.RowSandbox, Project: "alpha", Name: "mercury"}
	if got, want := keyOf(row), (sandboxKey{Project: "alpha", Name: "mercury"}); got != want {
		t.Errorf("keyOf(%+v) = %+v, want %+v", row, got, want)
	}
	// Two projects' sandboxes that share a name are different keys — which
	// is the whole reason the project rides along in the key.
	other := control.Row{Kind: control.RowSandbox, Project: "beta", Name: "mercury"}
	if keyOf(row) == keyOf(other) {
		t.Error("sandboxes of different projects must not share a key")
	}
}

// The leader is declared so the config shape is stable for rollout step 4,
// but nothing in step 3 dispatches it and it must not appear in help.
func TestLeaderIsDeclaredButNotAdvertised(t *testing.T) {
	k := NewKeyMap(nil)
	if len(k.Leader.Keys()) == 0 {
		t.Error("the leader binding should be declared")
	}
	for _, b := range k.ShortHelp() {
		if reflect.DeepEqual(b.Keys(), k.Leader.Keys()) {
			t.Error("the leader must not appear in short help until panes land")
		}
	}
	for _, col := range k.FullHelp() {
		for _, b := range col {
			if reflect.DeepEqual(b.Keys(), k.Leader.Keys()) {
				t.Error("the leader must not appear in full help until panes land")
			}
		}
	}
}
