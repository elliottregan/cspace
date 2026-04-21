// internal/tui/messages.go
package tui

import (
	"time"

	"github.com/elliottregan/cspace/internal/diagnostics"
)

// AgentsUpdatedMsg carries a fresh snapshot of all agents from GET /agents.
type AgentsUpdatedMsg []diagnostics.AgentSnapshot

// EventReceivedMsg carries one live event from the WebSocket stream.
type EventReceivedMsg diagnostics.Envelope

// ServicesUpdatedMsg carries fresh shared-service health from Docker.
type ServicesUpdatedMsg []ServiceStatus

// TickMsg fires on the 2-second HTTP poll interval.
type TickMsg time.Time

// WSStatusMsg signals a WebSocket connect or disconnect.
type WSStatusMsg struct{ Connected bool }
