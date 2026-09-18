package controlplane

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// The design's three cadences, and the budget each poll gets.
//
// The fast one probes every running sandbox's supervisor; control's own
// per-probe timeout is 800ms and the fan-out is bounded, so a 1s cadence
// with a 3s ceiling cannot pile up. The slow one pays for `container stats`
// (~2s) plus one `ss` exec per running sandbox, which is why it is 10s and
// why its ceiling is generous.
const (
	fastInterval   = 1 * time.Second
	mediumInterval = 2 * time.Second
	slowInterval   = 10 * time.Second

	fastTimeout   = 3 * time.Second
	mediumTimeout = 5 * time.Second
	slowTimeout   = 45 * time.Second

	// eventTail is how many events.ndjson lines the detail band reads.
	eventTail = 8

	// liveConcurrency bounds the fast ticker's fan-out, matching the bound
	// control uses inside a snapshot: a host with many sandboxes must not
	// open an unbounded burst of sockets every second.
	liveConcurrency = 8
)

// Data is the query surface the dashboard polls. Declared by the consumer
// and satisfied by *control.Client, so the model's tests inject canned data
// without a registry, a daemon or the `container` CLI.
type Data interface {
	SnapshotWith(ctx context.Context, opts control.SnapshotOpts) control.Snapshot
	AgentStatus(ctx context.Context, project, sandbox string) (control.AgentStatus, error)
	InteractiveState(project, sandbox string) control.InteractiveState
	Ports(ctx context.Context, project, sandbox string) ([]control.Port, error)
	Events(project, sandbox string, n int) ([]control.EventLine, error)
}

var _ Data = (*control.Client)(nil)

// One message per cadence, so each has its own in-flight guard and its own
// re-arm.
type (
	fastTickMsg   struct{ at time.Time }
	mediumTickMsg struct{ at time.Time }
	slowTickMsg   struct{ at time.Time }
)

// liveMsg carries the whole fast sample. The model replaces its map wholesale
// rather than merging into it: a sandbox that went away must lose its state,
// and a Model is copied on every Update, so nothing mutates a shared map.
type liveMsg struct{ states map[sandboxKey]liveState }

type snapshotMsg struct{ snap control.Snapshot }

// slowMsg carries the slow cadence's whole sample. portsErr is keyed like
// ports: one sandbox's failed probe is that sandbox's problem, and folding
// every target's error into one value made a single stopped sandbox blank
// the port list of every healthy one on the host until the next slow tick.
type slowMsg struct {
	snap     control.Snapshot
	ports    map[sandboxKey][]control.Port
	portsErr map[sandboxKey]error
}

// eventsMsg is one sandbox's event tail. key names the sandbox it was read
// for, captured when the Cmd was built: a tail requested for the previous
// selection can land after the one requested for the new selection, and the
// model has no other way to tell whose events it is holding.
type eventsMsg struct {
	key   sandboxKey
	lines []control.EventLine
	err   error
}

// sandboxTargets is every sandbox worth asking about: running or degraded.
// A stopped sandbox has no supervisor to probe, no session file to read, and
// control.Ports errors outright for one, so asking would turn an ordinary
// state into an error banner.
func sandboxTargets(rows []control.Row) []sandboxKey {
	var out []sandboxKey
	for _, r := range rows {
		if r.Kind != control.RowSandbox {
			continue
		}
		if r.State == control.StateRunning || r.State == control.StateDegraded {
			out = append(out, keyOf(r))
		}
	}
	return out
}

// liveCmd is the fast cadence: the supervisor's status and the interactive
// session's hook-written state for every running sandbox, concurrently.
func (m Model) liveCmd() tea.Cmd {
	data, targets := m.data, sandboxTargets(m.rows)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fastTimeout)
		defer cancel()

		states := make(map[sandboxKey]liveState, len(targets))
		var mu sync.Mutex
		sem := make(chan struct{}, liveConcurrency)
		var wg sync.WaitGroup
		for _, k := range targets {
			wg.Add(1)
			go func(k sandboxKey) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				// An unreachable supervisor is not an error here: control
				// reports it as AgentStatus{Reachable:false}, which is
				// exactly what the row should render.
				agent, _ := data.AgentStatus(ctx, k.Project, k.Name)
				inter := data.InteractiveState(k.Project, k.Name)
				mu.Lock()
				states[k] = liveState{Agent: agent, Interactive: inter}
				mu.Unlock()
			}(k)
		}
		wg.Wait()
		return liveMsg{states: states}
	}
}

// snapshotCmd is the medium cadence: the row set, without the ~2s stats
// sample the slow cadence pays for.
func (m Model) snapshotCmd() tea.Cmd {
	data := m.data
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), mediumTimeout)
		defer cancel()
		return snapshotMsg{snap: data.SnapshotWith(ctx, control.SnapshotOpts{SkipStats: true})}
	}
}

// slowCmd is the slow cadence: the stats-bearing snapshot and one Ports
// query per running sandbox. The port queries run in sequence — each is a
// `container exec` and they share one transport, so a fan-out would only
// queue inside the substrate.
func (m Model) slowCmd() tea.Cmd {
	data, targets := m.data, sandboxTargets(m.rows)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), slowTimeout)
		defer cancel()

		snap := data.SnapshotWith(ctx, control.SnapshotOpts{})
		ports := make(map[sandboxKey][]control.Port, len(targets))
		errs := make(map[sandboxKey]error)
		for _, k := range targets {
			p, err := data.Ports(ctx, k.Project, k.Name)
			if err != nil {
				errs[k] = err
				continue
			}
			ports[k] = p
		}
		return slowMsg{snap: snap, ports: ports, portsErr: errs}
	}
}

// eventsCmd reads the selected sandbox's event tail. Anything else selected
// clears the tail rather than leaving the previous sandbox's events under a
// new name.
func (m Model) eventsCmd() tea.Cmd {
	row := m.selectedRow()
	key := keyOf(row)
	if row.Kind != control.RowSandbox {
		return func() tea.Msg { return eventsMsg{key: key} }
	}
	data, project, name := m.data, row.Project, row.Name
	return func() tea.Msg {
		lines, err := data.Events(project, name, eventTail)
		return eventsMsg{key: key, lines: lines, err: err}
	}
}
