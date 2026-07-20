package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/elliottregan/cspace/internal/registry"
	"github.com/elliottregan/cspace/internal/substrate/applecontainer"
)

// probeTimeout bounds each control-port / daemon HTTP call. Short so a wedged
// supervisor degrades one row rather than stalling the whole poll.
const probeTimeout = 800 * time.Millisecond

// maxProbeConcurrency caps the status fan-out so a host with many sandboxes
// doesn't open an unbounded burst of sockets per tick.
const maxProbeConcurrency = 8

// Poller collects one Snapshot of host state.
type Poller interface {
	Poll(ctx context.Context) Snapshot
}

// containerLister is the slice of *applecontainer.Adapter the poller needs;
// an interface so tests inject a fake without the container CLI.
type containerLister interface {
	List(ctx context.Context) ([]applecontainer.ContainerSummary, error)
}

type realPoller struct {
	lister    containerLister
	registry  *registry.Registry
	daemonURL string
	client    *http.Client
	now       func() time.Time
	// browserCDPURL builds the CDP version-probe URL from a sidecar IP. A field
	// (not a hardcoded string) so tests can point it at an httptest server;
	// production uses the fixed :9222 DevTools port.
	browserCDPURL func(ip string) string
}

// NewPoller builds the real poller. daemonURL is the host daemon base
// (e.g. "http://127.0.0.1:6280"). now is injected for testable timestamps.
func NewPoller(lister containerLister, reg *registry.Registry, daemonURL string, now func() time.Time) *realPoller {
	return &realPoller{
		lister:        lister,
		registry:      reg,
		daemonURL:     daemonURL,
		client:        &http.Client{Timeout: probeTimeout},
		now:           now,
		browserCDPURL: func(ip string) string { return "http://" + ip + ":9222/json/version" },
	}
}

func (p *realPoller) Poll(ctx context.Context) Snapshot {
	containers, listErr := p.lister.List(ctx)
	entries, _ := p.registry.List() // missing file => empty slice, nil
	statuses := p.fetchStatuses(ctx, entries)
	browserHealth := p.fetchBrowserHealth(ctx, containers)
	daemon := p.fetchDaemon(ctx)
	return Correlate(p.now(), containers, entries, statuses, browserHealth, daemon, listErr)
}

// fetchBrowserHealth probes each running browser sidecar's Chrome DevTools
// endpoint (GET http://<ip>:9222/json/version) concurrently (bounded). Chrome's
// CDP HTTP endpoint accepts an IP-literal Host, so a host-side probe by the
// sidecar's vmnet IP works. Only successful probes land in the map; absence =>
// unreachable. Symmetric to fetchStatuses/fetchDaemon; keeps internal/tui free
// of any internal/cli import.
func (p *realPoller) fetchBrowserHealth(ctx context.Context, containers []applecontainer.ContainerSummary) map[string]BrowserHealth {
	out := make(map[string]BrowserHealth)
	var mu sync.Mutex
	sem := make(chan struct{}, maxProbeConcurrency)
	var wg sync.WaitGroup
	for _, c := range containers {
		if !strings.HasSuffix(c.Name, "-browser") || c.State != "running" || c.IP == "" {
			continue
		}
		wg.Add(1)
		go func(c applecontainer.ContainerSummary) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			h, ok := p.probeBrowser(ctx, c.IP)
			if !ok {
				return
			}
			mu.Lock()
			out[c.Name] = h
			mu.Unlock()
		}(c)
	}
	wg.Wait()
	return out
}

func (p *realPoller) probeBrowser(ctx context.Context, ip string) (BrowserHealth, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.browserCDPURL(ip), nil)
	if err != nil {
		return BrowserHealth{}, false
	}
	resp, err := p.client.Do(req)
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
func (p *realPoller) fetchStatuses(ctx context.Context, entries []registry.Entry) map[string]AgentStatus {
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
			st, ok := p.probeStatus(ctx, e)
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

func (p *realPoller) probeStatus(ctx context.Context, e registry.Entry) (AgentStatus, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.ControlURL+"/status", nil)
	if err != nil {
		return AgentStatus{}, false
	}
	if e.Token != "" {
		req.Header.Set("Authorization", "Bearer "+e.Token)
	}
	resp, err := p.client.Do(req)
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

func (p *realPoller) fetchDaemon(ctx context.Context) DaemonHealth {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.daemonURL+"/health", nil)
	if err != nil {
		return DaemonHealth{}
	}
	resp, err := p.client.Do(req)
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
