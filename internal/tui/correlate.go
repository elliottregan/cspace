package tui

import (
	"sort"
	"strings"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// Correlate folds the raw data sources into an ordered, grouped Snapshot.
// Pure — no I/O, no clock. Row order per project: header, then sandboxes
// (sorted by name) each followed by its compose sidecars, then the shared
// browser; unmatched containers (buildkit, orphans) trail as system rows.
func Correlate(
	now time.Time,
	containers []applecontainer.ContainerSummary,
	entries []registry.Entry,
	statuses map[string]AgentStatus,
	browserHealth map[string]BrowserHealth,
	daemon DaemonHealth,
	listErr error,
) Snapshot {
	byName := make(map[string]applecontainer.ContainerSummary, len(containers))
	for _, c := range containers {
		byName[c.Name] = c
	}
	consumed := make(map[string]bool, len(containers))

	// Group registry entries by project.
	projects := map[string][]registry.Entry{}
	for _, e := range entries {
		projects[e.Project] = append(projects[e.Project], e)
	}
	projectNames := make([]string, 0, len(projects))
	for p := range projects {
		projectNames = append(projectNames, p)
	}
	sort.Strings(projectNames)

	var rows []Row
	for _, project := range projectNames {
		rows = append(rows, Row{Kind: RowProject, Project: project, Name: project})
		es := projects[project]
		sort.Slice(es, func(i, j int) bool { return es[i].Name < es[j].Name })

		for _, e := range es {
			cname := containerName(project, e.Name)
			var running bool
			var ip string
			var mem int64
			var uptime time.Duration
			if c, ok := byName[cname]; ok {
				consumed[cname] = true
				running = c.State == "running"
				ip, mem = c.IP, c.MemoryB
				if !c.Started.IsZero() {
					uptime = now.Sub(c.Started)
				}
			}
			st := statuses[cname]
			state := StateStopped
			switch {
			case running && st.Reachable:
				state = StateRunning
			case running && e.State == "starting":
				state = StateBooting
			case running:
				state = StateDegraded
			case e.State == "starting":
				state = StateBooting
			}
			rows = append(rows, Row{
				Kind:       RowSandbox,
				Project:    project,
				Name:       e.Name,
				Container:  cname,
				State:      state,
				IP:         ip,
				MemoryB:    mem,
				Uptime:     uptime,
				Agent:      st,
				ControlURL: e.ControlURL,
				Token:      e.Token,
				Selectable: true,
			})

			// Nest compose sidecars: cspace-<project>-<name>-<suffix>.
			prefix := cname + "-"
			var sidecars []applecontainer.ContainerSummary
			for _, sc := range containers {
				if consumed[sc.Name] {
					continue
				}
				if strings.HasPrefix(sc.Name, prefix) {
					sidecars = append(sidecars, sc)
				}
			}
			sort.Slice(sidecars, func(i, j int) bool { return sidecars[i].Name < sidecars[j].Name })
			for _, sc := range sidecars {
				consumed[sc.Name] = true
				rows = append(rows, Row{
					Kind:      RowSidecar,
					Project:   project,
					Name:      strings.TrimPrefix(sc.Name, "cspace-"+project+"-"),
					Container: sc.Name,
					State:     sidecarState(sc.State),
					IP:        sc.IP,
					MemoryB:   sc.MemoryB,
				})
			}
		}

		// Project-level shared browser sidecar.
		bname := browserContainerName(project)
		if bc, ok := byName[bname]; ok {
			consumed[bname] = true
			rows = append(rows, Row{
				Kind:       RowBrowser,
				Project:    project,
				Name:       "browser (shared)",
				Container:  bname,
				State:      sidecarState(bc.State),
				IP:         bc.IP,
				MemoryB:    bc.MemoryB,
				Browser:    browserHealth[bname],
				Selectable: true,
			})
		}
	}

	// System / unmatched containers (buildkit, orphaned cspace-*), sorted.
	var system []applecontainer.ContainerSummary
	for _, c := range containers {
		if !consumed[c.Name] {
			system = append(system, c)
		}
	}
	sort.Slice(system, func(i, j int) bool { return system[i].Name < system[j].Name })
	for _, c := range system {
		rows = append(rows, Row{
			Kind:      RowSystem,
			Name:      c.Name,
			Container: c.Name,
			State:     sidecarState(c.State),
			IP:        c.IP,
			MemoryB:   c.MemoryB,
		})
	}

	return Snapshot{Rows: rows, Daemon: daemon, Err: listErr, TakenAt: now}
}

// sidecarState maps a raw container status string to a RowState (sidecars and
// system rows have no supervisor, so only running/stopped apply).
func sidecarState(status string) RowState {
	if status == "running" {
		return StateRunning
	}
	return StateStopped
}
