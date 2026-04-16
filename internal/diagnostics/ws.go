package diagnostics

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/websocket"
)

// WSMessage is the envelope for client→server messages over the WebSocket.
type WSMessage struct {
	// Subscribe changes the event filter. ["*"] = all, ["mercury", "venus"] = specific.
	Subscribe []string `json:"subscribe,omitempty"`

	// Cmd relays a command to a supervisor socket (e.g., "interrupt", "send").
	Cmd   string `json:"cmd,omitempty"`
	Agent string `json:"agent,omitempty"`
	Text  string `json:"text,omitempty"`
}

// WSReply is the envelope for server→client messages.
type WSReply struct {
	Type  string          `json:"type"`            // "event", "agents", "ack", "error"
	Event json.RawMessage `json:"event,omitempty"` // Raw Envelope JSON for type=event.
	Data  interface{}     `json:"data,omitempty"`  // For type=agents, type=ack.
	Error string          `json:"error,omitempty"` // For type=error.
}

// NewWSHandler returns an http.Handler that upgrades to WebSocket and
// streams events from the hub.
func NewWSHandler(hub *Hub, msgDir string) http.Handler {
	return websocket.Handler(func(conn *websocket.Conn) {
		sub := NewSubscriber(256)
		hub.Subscribe(sub)
		defer hub.Unsubscribe(sub)
		defer func() { _ = conn.Close() }()

		// Send initial agent list.
		agents := hub.Agents()
		sendReply(conn, WSReply{Type: "agents", Data: agents})

		// Read loop: process client messages in a goroutine.
		clientDone := make(chan struct{})
		go func() {
			defer close(clientDone)
			for {
				var msg WSMessage
				if err := websocket.JSON.Receive(conn, &msg); err != nil {
					return
				}
				handleClientMessage(conn, hub, sub, msgDir, msg)
			}
		}()

		// Write loop: forward hub events to the client.
		for {
			select {
			case data, ok := <-sub.Events:
				if !ok {
					return
				}
				reply := WSReply{Type: "event", Event: json.RawMessage(data)}
				if err := websocket.JSON.Send(conn, reply); err != nil {
					return
				}
			case <-clientDone:
				return
			}
		}
	})
}

func handleClientMessage(conn *websocket.Conn, hub *Hub, sub *Subscriber, msgDir string, msg WSMessage) {
	if msg.Subscribe != nil {
		sub.SetFilter(msg.Subscribe)
		sendReply(conn, WSReply{Type: "ack", Data: map[string]interface{}{
			"subscribed": msg.Subscribe,
		}})
		return
	}

	if msg.Cmd != "" && msg.Agent != "" {
		result := relaySupervisorCommand(msgDir, msg)
		sendReply(conn, result)
		return
	}
}

func sendReply(conn *websocket.Conn, reply WSReply) {
	if err := websocket.JSON.Send(conn, reply); err != nil {
		log.Printf("[diagnostics] ws send: %v", err)
	}
}

// relaySupervisorCommand forwards a command from a WS client to an agent's
// supervisor socket and returns the result.
func relaySupervisorCommand(msgDir string, msg WSMessage) WSReply {
	sockPath := supervisorSocketPath(msgDir, msg.Agent)

	var req interface{}
	switch msg.Cmd {
	case "interrupt":
		req = map[string]string{"cmd": "interrupt"}
	case "send":
		if msg.Text == "" {
			return WSReply{Type: "error", Error: "text required for send command"}
		}
		req = map[string]string{"cmd": "send_user_message", "text": msg.Text}
	case "status":
		req = map[string]string{"cmd": "status"}
	default:
		return WSReply{Type: "error", Error: "unknown command: " + msg.Cmd}
	}

	reply, err := socketRequest(sockPath, req)
	if err != nil {
		return WSReply{Type: "error", Error: err.Error()}
	}
	return WSReply{Type: "ack", Data: reply}
}

func supervisorSocketPath(msgDir, instance string) string {
	if instance == "_coordinator" {
		return msgDir + "/_coordinator/supervisor.sock"
	}
	return msgDir + "/" + instance + "/supervisor.sock"
}

// socketRequest sends a JSON command to a Unix socket and reads the reply.
func socketRequest(sockPath string, request interface{}) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))

	data, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(data, '\n')); err != nil {
		return nil, err
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(buf[:n]), nil
}
