package devcontainer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestForwardPortFromInt(t *testing.T) {
	var fp ForwardPort
	if err := json.Unmarshal([]byte(`5173`), &fp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fp.Port != 5173 {
		t.Fatalf("port=%d, want 5173", fp.Port)
	}
}

func TestForwardPortFromObject(t *testing.T) {
	var fp ForwardPort
	if err := json.Unmarshal([]byte(`{"port": 4173, "host": "all"}`), &fp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if fp.Port != 4173 || fp.Host != "all" {
		t.Fatalf("got %+v", fp)
	}
}

func TestForwardPortStringRejected(t *testing.T) {
	var fp ForwardPort
	err := json.Unmarshal([]byte(`"5173:5173"`), &fp)
	if err == nil || !strings.Contains(err.Error(), "host-port mapping") {
		t.Fatalf("want host-port-mapping error, got %v", err)
	}
}
