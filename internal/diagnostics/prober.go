package diagnostics

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Prober periodically checks supervisor sockets for liveness and updates
// the hub's agent states.
type Prober struct {
	hub      *Hub
	msgDir   string // e.g. /logs/messages
	interval time.Duration
	timeout  time.Duration
	done     chan struct{}
}

// NewProber creates a socket prober.
func NewProber(hub *Hub, msgDir string, interval, timeout time.Duration) *Prober {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Prober{
		hub:      hub,
		msgDir:   msgDir,
		interval: interval,
		timeout:  timeout,
		done:     make(chan struct{}),
	}
}

// Run starts the probing loop. Blocks until Stop is called.
func (p *Prober) Run() {
	// Initial probe.
	p.probeAll()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.probeAll()
			p.hub.CheckStuckAgents()
		}
	}
}

// Stop signals the prober to exit.
func (p *Prober) Stop() {
	close(p.done)
}

// probeAll discovers all supervisor sockets and probes each one.
func (p *Prober) probeAll() {
	entries, err := os.ReadDir(p.msgDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		sockPath := filepath.Join(p.msgDir, name, "supervisor.sock")
		if _, err := os.Stat(sockPath); err != nil {
			continue
		}

		// Probe this socket.
		instance := name
		if name == "_coordinator" {
			instance = "_coordinator"
		}

		alive := p.probeSocket(sockPath)
		p.hub.UpdateSocketStatus(instance, alive)
	}
}

// socketStatusReply is the expected shape of a status response.
type socketStatusReply struct {
	OK     bool `json:"ok"`
	Status *struct {
		Role           string `json:"role"`
		Instance       string `json:"instance"`
		SessionID      string `json:"sessionId"`
		Turns          int    `json:"turns"`
		LastActivityMs int64  `json:"lastActivityMs"`
	} `json:"status,omitempty"`
}

// probeSocket connects to a supervisor socket and sends a status command.
// Returns true if the socket responds with ok:true.
func (p *Prober) probeSocket(sockPath string) bool {
	conn, err := net.DialTimeout("unix", sockPath, p.timeout)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(p.timeout))

	req, _ := json.Marshal(map[string]string{"cmd": "status"})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return false
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return false
	}

	// Find first newline — response is NDJSON-framed.
	data := buf[:n]
	if idx := strings.IndexByte(string(data), '\n'); idx >= 0 {
		data = data[:idx]
	}

	var reply socketStatusReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return false
	}
	return reply.OK
}
