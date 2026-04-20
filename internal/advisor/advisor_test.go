package advisor

import (
	"testing"

	"github.com/elliottregan/cspace/internal/config"
)

func TestBuildLaunchParams(t *testing.T) {
	cfg := &config.Config{
		ProjectRoot: "/tmp/proj",
		AssetsDir:   "/opt/assets",
		Advisors: map[string]config.AdvisorConfig{
			"decision-maker": {
				Model:      "claude-opus-4-7",
				Effort:     "max",
				BaseBranch: "main",
			},
		},
	}

	params, err := BuildLaunchParams(cfg, "decision-maker")
	if err != nil {
		t.Fatalf("BuildLaunchParams: %v", err)
	}
	if params.ModelOverride != "claude-opus-4-7" {
		t.Errorf("Model: %s", params.ModelOverride)
	}
	if params.EffortOverride != "max" {
		t.Errorf("Effort: %s", params.EffortOverride)
	}
	if len(params.AdvisorNames) != 1 || params.AdvisorNames[0] != "decision-maker" {
		t.Errorf("AdvisorNames: %v", params.AdvisorNames)
	}
	if params.Name != "decision-maker" {
		t.Errorf("Name: %s", params.Name)
	}
}

func TestBuildLaunchParamsUnknownAdvisor(t *testing.T) {
	cfg := &config.Config{Advisors: map[string]config.AdvisorConfig{}}
	_, err := BuildLaunchParams(cfg, "missing")
	if err == nil {
		t.Fatal("expected error for unknown advisor")
	}
}
