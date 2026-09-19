package controlplane

import (
	tea "charm.land/bubbletea/v2"

	"github.com/elliottregan/cspace/internal/control"
)

// supervisor is the read-only view over a sandbox's event log. Task 6 of the
// panes plan fills it in and replaces this whole file; this is the shape the
// tab bookkeeping, the main area (Task 3) and the key routing (Task 4) need
// in order to compile, and `make check` has to be green after every task.
type supervisor struct{ width, height int }

func newSupervisor(width int) *supervisor { return &supervisor{width: width} }

func (s *supervisor) resize(width, height int) { s.width, s.height = width, height }

//nolint:unused // filled in by Task 6, which is the first caller
func (s *supervisor) setEvents([]control.EventLine) {}

//nolint:unused // filled in by Task 3, which is the first caller
func (s *supervisor) view(width, height int) string { return "" }

func (m Model) supervisorEventsCmd(*tab) tea.Cmd { return nil }

//nolint:unused // filled in by Task 4, which is the first caller
func (m Model) handleSupervisorKey(*tab, tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	return m, nil
}
