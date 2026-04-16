package diagnostics

import (
	"encoding/json"
	"log"
	"sync"
	"time"
)

// Hub is the central state manager. It owns all AgentState instances,
// dispatches events from the tailer, and fans out to WebSocket subscribers.
type Hub struct {
	mu     sync.RWMutex
	agents map[string]*AgentState

	// Subscriber management.
	subMu       sync.RWMutex
	subscribers map[*Subscriber]struct{}

	// Config.
	maxRecentPerAgent int
	stuckThreshold    time.Duration
}

// HubConfig configures the diagnostic hub.
type HubConfig struct {
	MaxRecentPerAgent int           // Ring buffer size per agent (default 200)
	StuckThreshold    time.Duration // Mark agent "stuck" after this idle period (default 5m)
}

// NewHub creates a new diagnostic hub.
func NewHub(cfg HubConfig) *Hub {
	if cfg.MaxRecentPerAgent <= 0 {
		cfg.MaxRecentPerAgent = 200
	}
	if cfg.StuckThreshold <= 0 {
		cfg.StuckThreshold = 5 * time.Minute
	}
	return &Hub{
		agents:            make(map[string]*AgentState),
		subscribers:       make(map[*Subscriber]struct{}),
		maxRecentPerAgent: cfg.MaxRecentPerAgent,
		stuckThreshold:    cfg.StuckThreshold,
	}
}

// IngestEvent processes an event from the tailer. Creates the agent
// state on first sight. Thread-safe.
func (h *Hub) IngestEvent(env Envelope) {
	if env.Instance == "" {
		return
	}

	h.mu.Lock()
	agent, ok := h.agents[env.Instance]
	if !ok {
		agent = NewAgentState(env.Instance, env.Role, h.maxRecentPerAgent)
		h.agents[env.Instance] = agent
	}
	h.mu.Unlock()

	agent.IngestEvent(env)
	h.broadcast(env)
}

// UpdateSocketStatus updates the liveness flag for an agent.
func (h *Hub) UpdateSocketStatus(instance string, alive bool) {
	h.mu.RLock()
	agent, ok := h.agents[instance]
	h.mu.RUnlock()
	if !ok {
		return
	}

	agent.mu.Lock()
	agent.SocketAlive = alive
	// If socket is dead and agent was active, mark as exited.
	if !alive && agent.Status == StatusActive {
		agent.Status = StatusExited
	}
	agent.mu.Unlock()
}

// CheckStuckAgents marks agents as stuck if they've been idle too long
// while their socket is still alive (i.e., not exited, just unresponsive).
func (h *Hub) CheckStuckAgents() {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, agent := range h.agents {
		agent.mu.Lock()
		if agent.Status == StatusActive && agent.SocketAlive {
			if time.Since(agent.LastActivity) > h.stuckThreshold {
				agent.Status = StatusStuck
			}
		}
		agent.mu.Unlock()
	}
}

// Agents returns snapshots of all known agents.
func (h *Hub) Agents() []AgentSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]AgentSnapshot, 0, len(h.agents))
	for _, agent := range h.agents {
		result = append(result, agent.Snapshot())
	}
	return result
}

// Agent returns a snapshot of one agent, or nil if not found.
func (h *Hub) Agent(instance string) *AgentSnapshot {
	h.mu.RLock()
	agent, ok := h.agents[instance]
	h.mu.RUnlock()
	if !ok {
		return nil
	}
	snap := agent.Snapshot()
	return &snap
}

// RecentEvents returns the last N events for an agent.
func (h *Hub) RecentEvents(instance string, limit int) []Envelope {
	h.mu.RLock()
	agent, ok := h.agents[instance]
	h.mu.RUnlock()
	if !ok {
		return nil
	}
	return agent.RecentEvents(limit)
}

// --- WebSocket subscriber fan-out ---

// Subscriber represents a WebSocket client subscription.
type Subscriber struct {
	// Filter: if empty or contains "*", receive all events.
	// Otherwise, only events from listed instances.
	Instances map[string]bool

	// Channel for outgoing events. Hub writes, WS handler reads.
	Events chan []byte

	mu sync.RWMutex
}

// NewSubscriber creates a subscriber with a buffered event channel.
func NewSubscriber(bufSize int) *Subscriber {
	if bufSize <= 0 {
		bufSize = 256
	}
	return &Subscriber{
		Instances: make(map[string]bool),
		Events:    make(chan []byte, bufSize),
	}
}

// SetFilter updates the subscription filter. Pass "*" for all agents.
func (s *Subscriber) SetFilter(instances []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Instances = make(map[string]bool, len(instances))
	for _, inst := range instances {
		s.Instances[inst] = true
	}
}

// wantsEvent checks if this subscriber wants events for the given instance.
func (s *Subscriber) wantsEvent(instance string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.Instances) == 0 || s.Instances["*"] {
		return true
	}
	return s.Instances[instance]
}

// Subscribe adds a subscriber to the fan-out.
func (h *Hub) Subscribe(sub *Subscriber) {
	h.subMu.Lock()
	h.subscribers[sub] = struct{}{}
	h.subMu.Unlock()
}

// Unsubscribe removes a subscriber.
func (h *Hub) Unsubscribe(sub *Subscriber) {
	h.subMu.Lock()
	delete(h.subscribers, sub)
	h.subMu.Unlock()
}

// broadcast sends an event to all matching subscribers.
func (h *Hub) broadcast(env Envelope) {
	data, err := json.Marshal(env)
	if err != nil {
		log.Printf("[diagnostics] marshal broadcast: %v", err)
		return
	}

	h.subMu.RLock()
	defer h.subMu.RUnlock()

	for sub := range h.subscribers {
		if !sub.wantsEvent(env.Instance) {
			continue
		}
		select {
		case sub.Events <- data:
		default:
			// Slow subscriber — drop event rather than blocking the tailer.
		}
	}
}
