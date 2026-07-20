package tui

// contextual predicates — pure, tested directly. A running container means
// State is anything other than StateStopped.

func canAttach(r Row) bool { return r.Kind == RowSandbox && r.State != StateStopped }
func canDown(r Row) bool   { return r.Kind == RowSandbox && r.State != StateStopped }
func canSend(r Row) bool   { return r.Kind == RowSandbox && r.Agent.Reachable }
func canInterrupt(r Row) bool {
	return r.Kind == RowSandbox && r.Agent.Reachable && r.Agent.State == "working"
}
func canBrowser(r Row) bool { return r.Kind == RowBrowser || r.Kind == RowSandbox }
