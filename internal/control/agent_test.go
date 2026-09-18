package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentStatusReportsSupervisorFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/status" || req.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "session": "primary", "state": "working",
			"lastEventTs": "2026-09-17T18:40:12Z", "lastEventType": "assistant",
			"lastEventSubtype": "", "queueDepth": 2,
		})
	}))
	defer srv.Close()

	c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", srv.URL, "tok")})
	got, err := c.AgentStatus(context.Background(), "alpha", "mercury")
	if err != nil {
		t.Fatalf("AgentStatus: %v", err)
	}
	if !got.Reachable || got.State != "working" || got.Session != "primary" || got.QueueDepth != 2 {
		t.Errorf("status = %+v", got)
	}
	if got.LastEventType != "assistant" || got.LastEventTs != "2026-09-17T18:40:12Z" {
		t.Errorf("last event = %+v", got)
	}
}

func TestAgentStatusUnreachableIsNotAnError(t *testing.T) {
	// Port 1 refuses instantly: an unreachable supervisor degrades the value,
	// it does not fail the query — the caller renders "degraded", not an error.
	c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", "http://127.0.0.1:1", "tok")})
	got, err := c.AgentStatus(context.Background(), "alpha", "mercury")
	if err != nil {
		t.Fatalf("an unreachable supervisor must not be an error, got %v", err)
	}
	if got.Reachable {
		t.Errorf("status = %+v, want Reachable false", got)
	}
}

func TestAgentStatusUnknownSandboxErrors(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", "http://127.0.0.1:1", "")})
	if _, err := c.AgentStatus(context.Background(), "alpha", "nope"); err == nil {
		t.Error("an unregistered sandbox must error")
	}
}

func TestSendPostsSessionAndText(t *testing.T) {
	var gotPath, gotAuth, gotCT string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath, gotAuth, gotCT = req.URL.Path, req.Header.Get("Authorization"), req.Header.Get("Content-Type")
		_ = json.NewDecoder(req.Body).Decode(&gotBody)
		w.WriteHeader(200)
		_, _ = w.Write([]byte("queued"))
	}))
	defer srv.Close()

	c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", srv.URL, "tok")})
	if err := c.Send(context.Background(), "alpha", "mercury", "", "hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/send" || gotAuth != "Bearer tok" || gotCT != "application/json" {
		t.Errorf("request: path=%q auth=%q ct=%q", gotPath, gotAuth, gotCT)
	}
	// An empty session argument means the supervisor's own session id.
	if gotBody["session"] != DefaultSession || gotBody["text"] != "hello" {
		t.Errorf("body = %+v", gotBody)
	}
}

func TestSendSurfacesServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": "queue closed"})
	}))
	defer srv.Close()

	c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", srv.URL, "tok")})
	err := c.Send(context.Background(), "alpha", "mercury", "primary", "hello")
	if err == nil || !strings.Contains(err.Error(), "queue closed") {
		t.Errorf("err = %v, want it to carry the server's error text", err)
	}
}

func TestInterruptStatusHandling(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr string // "" means the call must succeed
	}{
		{"2xx is success", 200, `{"ok":true}`, ""},
		{"409 no active task is benign", 409, `{"ok":false,"error":"no active task"}`, ""},
		{"500 surfaces the server's error text", 500, `{"ok":false,"error":"boom"}`, "boom"},
		{"non-JSON body falls back to raw text", 503, "upstream gone", "upstream gone"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				gotPath = req.URL.Path
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			c := New(Options{Containers: &fakeContainers{}, Entries: writeRegistry(t, "alpha", "mercury", srv.URL, "tok")})
			err := c.Interrupt(context.Background(), "alpha", "mercury")
			if gotPath != "/interrupt" {
				t.Errorf("path = %q, want /interrupt", gotPath)
			}
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("want success, got %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// A Client built with no EntryStore must fail closed on every action that
// resolves a sandbox through lookup, rather than nil-panic on
// c.entries.Lookup.
func TestActionsErrorWithoutEntryStore(t *testing.T) {
	c := New(Options{Containers: &fakeContainers{}})

	if _, err := c.AgentStatus(context.Background(), "alpha", "mercury"); !errors.Is(err, ErrNoEntryStore) {
		t.Errorf("AgentStatus err = %v, want ErrNoEntryStore", err)
	}
	if err := c.Send(context.Background(), "alpha", "mercury", "", "hi"); !errors.Is(err, ErrNoEntryStore) {
		t.Errorf("Send err = %v, want ErrNoEntryStore", err)
	}
	if err := c.Interrupt(context.Background(), "alpha", "mercury"); !errors.Is(err, ErrNoEntryStore) {
		t.Errorf("Interrupt err = %v, want ErrNoEntryStore", err)
	}
}

func TestErrorText(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"supervisor error envelope", `{"ok":false,"error":"no active task"}`, "no active task"},
		{"plain text body", "  not found\n", "not found"},
		{"json without an error field", `{"ok":true}`, `{"ok":true}`},
	}
	for _, tc := range cases {
		if got := ErrorText([]byte(tc.body)); got != tc.want {
			t.Errorf("%s: ErrorText(%q) = %q, want %q", tc.name, tc.body, got, tc.want)
		}
	}
}
