package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// Snapshotter collects one Snapshot of host state. The dashboard takes this
// interface rather than *Client so its model tests can inject a canned
// snapshot.
type Snapshotter interface {
	Snapshot(ctx context.Context) Snapshot
}

var _ Snapshotter = (*Client)(nil)

// SnapshotOpts tunes one Snapshot.
type SnapshotOpts struct {
	// SkipStats omits the `container stats` sample. That sample costs ~2s
	// against Apple Container 1.3 — two orders of magnitude more than every
	// other source in a snapshot — so a caller polling on a short cadence
	// asks for it on a slower cadence of its own instead. Rows then carry
	// MemoryUsedB 0, exactly as a failed stats probe already leaves them,
	// and render their cap alone.
	SkipStats bool
}

// Snapshot reports every sandbox on the host grouped by project: lifecycle,
// memory cap and usage, uptime, nested compose sidecars, the project's
// browser sidecar and its health, and daemon health.
func (c *Client) Snapshot(ctx context.Context) Snapshot {
	return c.SnapshotWith(ctx, SnapshotOpts{})
}

// SnapshotWith is Snapshot with the options above.
func (c *Client) SnapshotWith(ctx context.Context, opts SnapshotOpts) Snapshot {
	if c.containers == nil {
		return Snapshot{Err: ErrNoContainerCLI, TakenAt: c.now()}
	}
	if c.entries == nil {
		return Snapshot{Err: ErrNoEntryStore, TakenAt: c.now()}
	}
	containers, listErr := c.containers.List(ctx)
	entries, _ := c.entries.List() // missing file => empty slice, nil

	// When stats are wanted they run concurrently with the HTTP probes
	// rather than adding their ~2s to them: run sequentially they would
	// push a snapshot toward the caller's context ceiling and start timing
	// the whole thing out. A nil map is a legal read target, so the
	// skip path needs no other branch.
	var (
		stats   map[string]applecontainer.ContainerStats
		statsWG sync.WaitGroup
	)
	if !opts.SkipStats {
		statsWG.Add(1)
		go func() {
			defer statsWG.Done()
			stats = c.fetchStats(ctx)
		}()
	}

	statuses := c.fetchStatuses(ctx, entries)
	browserHealth := c.fetchBrowserHealth(ctx, containers)
	daemon := c.fetchDaemon(ctx)
	statsWG.Wait()

	return Correlate(c.now(), containers, entries, statuses, browserHealth, stats, daemon, listErr)
}

// fetchStats samples live per-container resource usage. A stats failure is
// swallowed to an empty map rather than surfaced: usage is decoration on rows
// that are already correct without it, so a wedged stats call must not blank
// the dashboard the way a failed `container ls` legitimately does.
func (c *Client) fetchStats(ctx context.Context) map[string]applecontainer.ContainerStats {
	out := map[string]applecontainer.ContainerStats{}
	samples, err := c.containers.Stats(ctx)
	if err != nil {
		return out
	}
	for _, s := range samples {
		out[s.Name] = s
	}
	return out
}

// fetchBrowserHealth probes each running browser sidecar's Chrome DevTools
// endpoint (GET http://<ip>:9222/json/version) concurrently (bounded).
// Chrome's CDP HTTP endpoint accepts an IP-literal Host, so a host-side probe
// by the sidecar's vmnet IP works. Only successful probes land in the map;
// absence => unreachable.
func (c *Client) fetchBrowserHealth(ctx context.Context, containers []applecontainer.ContainerSummary) map[string]BrowserHealth {
	out := make(map[string]BrowserHealth)
	var mu sync.Mutex
	sem := make(chan struct{}, maxProbeConcurrency)
	var wg sync.WaitGroup
	for _, ct := range containers {
		if !strings.HasSuffix(ct.Name, "-browser") || ct.State != "running" || ct.IP == "" {
			continue
		}
		wg.Add(1)
		go func(ct applecontainer.ContainerSummary) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			h, ok := c.probeBrowser(ctx, ct.IP)
			if !ok {
				return
			}
			mu.Lock()
			out[ct.Name] = h
			mu.Unlock()
		}(ct)
	}
	wg.Wait()
	return out
}

func (c *Client) probeBrowser(ctx context.Context, ip string) (BrowserHealth, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.browserCDPURL(ip), nil)
	if err != nil {
		return BrowserHealth{}, false
	}
	resp, err := c.probeClient.Do(req)
	if err != nil {
		return BrowserHealth{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return BrowserHealth{}, false
	}
	var body struct {
		Browser string `json:"Browser"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return BrowserHealth{Reachable: true, Version: body.Browser}, true
}

// fetchStatuses probes each entry's GET /status concurrently (bounded). Only
// successful probes land in the map; absence => unreachable (Correlate reads
// that as degraded when the container is running, stopped otherwise).
func (c *Client) fetchStatuses(ctx context.Context, entries []registry.Entry) map[string]AgentStatus {
	out := make(map[string]AgentStatus, len(entries))
	var mu sync.Mutex
	sem := make(chan struct{}, maxProbeConcurrency)
	var wg sync.WaitGroup
	for _, e := range entries {
		if e.ControlURL == "" {
			continue
		}
		wg.Add(1)
		go func(e registry.Entry) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			st, ok := c.probeStatus(ctx, e)
			if !ok {
				return
			}
			mu.Lock()
			out[containerName(e.Project, e.Name)] = st
			mu.Unlock()
		}(e)
	}
	wg.Wait()
	return out
}

// probeStatus is one authenticated GET /status against a sandbox's control
// port. ok is false when the probe failed (timeout, refused, non-2xx,
// undecodable body) — the caller decides what that means.
func (c *Client) probeStatus(ctx context.Context, e registry.Entry) (AgentStatus, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.ControlURL+"/status", nil)
	if err != nil {
		return AgentStatus{}, false
	}
	if e.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.Token)
	}
	resp, err := c.probeClient.Do(req)
	if err != nil {
		return AgentStatus{}, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return AgentStatus{}, false
	}
	var body struct {
		State            string `json:"state"`
		Session          string `json:"session"`
		QueueDepth       int    `json:"queueDepth"`
		LastEventType    string `json:"lastEventType"`
		LastEventSubtype string `json:"lastEventSubtype"`
		LastEventTs      string `json:"lastEventTs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return AgentStatus{}, false
	}
	return AgentStatus{
		Reachable:        true,
		State:            body.State,
		Session:          body.Session,
		QueueDepth:       body.QueueDepth,
		LastEventType:    body.LastEventType,
		LastEventSubtype: body.LastEventSubtype,
		LastEventTs:      body.LastEventTs,
	}, true
}

func (c *Client) fetchDaemon(ctx context.Context) DaemonHealth {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.daemonURL+"/health", nil)
	if err != nil {
		return DaemonHealth{}
	}
	resp, err := c.probeClient.Do(req)
	if err != nil {
		return DaemonHealth{}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return DaemonHealth{}
	}
	body, _ := io.ReadAll(resp.Body)
	var h struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(body, &h)
	return DaemonHealth{Reachable: true, Version: h.Version}
}
