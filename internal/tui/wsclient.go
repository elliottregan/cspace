// internal/tui/wsclient.go
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/net/websocket"

	"github.com/elliottregan/cspace/internal/diagnostics"
)

// WSClient manages the WebSocket connection to the diagnostics server.
type WSClient struct {
	addr    string
	program *tea.Program
}

// NewWSClient constructs a WebSocket client for the given address.
func NewWSClient(addr string, program *tea.Program) *WSClient {
	return &WSClient{addr: addr, program: program}
}

// Run connects and pumps events into the Bubbletea program. Reconnects
// automatically on disconnect. Blocks until ctx is cancelled.
func (c *WSClient) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_ = c.connect(ctx)
		if ctx.Err() != nil {
			return
		}

		c.program.Send(WSStatusMsg{Connected: false})
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *WSClient) connect(ctx context.Context) error {
	url := fmt.Sprintf("ws://%s/ws", c.addr)
	origin := fmt.Sprintf("http://%s/", c.addr)

	ws, err := websocket.Dial(url, "", origin)
	if err != nil {
		return err
	}
	defer ws.Close()

	c.program.Send(WSStatusMsg{Connected: true})

	sub, err := json.Marshal(diagnostics.WSMessage{Subscribe: []string{"*"}})
	if err != nil {
		return err
	}
	if _, err := ws.Write(sub); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		ws.Close()
	}()

	for {
		var raw []byte
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			return err
		}
		var reply diagnostics.WSReply
		if err := json.Unmarshal(raw, &reply); err != nil {
			continue
		}
		if reply.Type == "event" && reply.Event != nil {
			var env diagnostics.Envelope
			if err := json.Unmarshal(reply.Event, &env); err != nil {
				continue
			}
			c.program.Send(EventReceivedMsg(env))
		}
	}
}
