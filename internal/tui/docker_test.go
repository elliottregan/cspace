package tui

import (
	"testing"
)

func TestParseRunning(t *testing.T) {
	tests := []struct {
		output string
		want   bool
	}{
		{"true\n", true},
		{"false\n", false},
		{"", false},
		{"error\n", false},
	}
	for _, tt := range tests {
		got := parseRunning(tt.output)
		if got != tt.want {
			t.Errorf("parseRunning(%q) = %v, want %v", tt.output, got, tt.want)
		}
	}
}

func TestSharedContainerDefs(t *testing.T) {
	if len(sharedContainerDefs) == 0 {
		t.Fatal("sharedContainerDefs must not be empty")
	}
	for _, d := range sharedContainerDefs {
		if d.name == "" {
			t.Error("container def has empty name")
		}
		if d.label == "" {
			t.Error("container def has empty label")
		}
	}
}
