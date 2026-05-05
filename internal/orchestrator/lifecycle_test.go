package orchestrator

import (
	"context"
	"fmt"
	"testing"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
	"github.com/elliottregan/cspace/internal/devcontainer"
)

type stubSubstrate struct {
	runs  []ServiceSpec
	stops []string
	execs map[string][]string
	ips   map[string]string
}

func newStub() *stubSubstrate {
	return &stubSubstrate{
		execs: map[string][]string{},
		ips:   map[string]string{},
	}
}

func (s *stubSubstrate) Run(_ context.Context, spec ServiceSpec) (string, error) {
	s.runs = append(s.runs, spec)
	ip := fmt.Sprintf("192.168.64.%d", 40+len(s.runs))
	s.ips[spec.Name] = ip
	return ip, nil
}

func (s *stubSubstrate) Exec(_ context.Context, name string, cmd []string) (string, error) {
	s.execs[name] = cmd
	return "", nil
}

func (s *stubSubstrate) Stop(_ context.Context, name string) error {
	s.stops = append(s.stops, name)
	return nil
}

func (s *stubSubstrate) IP(_ context.Context, name string) (string, error) {
	if ip, ok := s.ips[name]; ok {
		return ip, nil
	}
	return "", fmt.Errorf("no IP for %s", name)
}

func TestUpSpawnsAllNonSandboxServices(t *testing.T) {
	stub := newStub()
	orch := &Orchestration{
		Sandbox: "mercury",
		Project: "rr",
		Plan: &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{Service: "app"},
			Compose: &v2.Project{
				Services: map[string]*v2.Service{
					"app":       {Name: "app", Image: "alpine"},
					"backend":   {Name: "backend", Image: "ghcr.io/get-convex/convex-backend:latest"},
					"dashboard": {Name: "dashboard", Image: "ghcr.io/get-convex/convex-dashboard:latest"},
				},
			},
			Service: "app",
		},
		Substrate: stub,
	}
	if err := orch.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(stub.runs) != 2 {
		t.Fatalf("expected 2 sidecars, got %d (%+v)", len(stub.runs), stub.runs)
	}
	for _, r := range stub.runs {
		if r.Name == orch.containerName("app") {
			t.Fatalf("sandbox service spawned as sidecar")
		}
	}
}

func TestUpNoComposeIsNoOp(t *testing.T) {
	stub := newStub()
	orch := &Orchestration{
		Sandbox: "m", Project: "p",
		Plan: &devcontainer.Plan{Devcontainer: &devcontainer.Config{Image: "alpine"}},
		Substrate: stub,
	}
	if err := orch.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(stub.runs) != 0 {
		t.Fatalf("expected no spawns, got %v", stub.runs)
	}
}

func TestContainerNameFormat(t *testing.T) {
	o := &Orchestration{Sandbox: "mercury", Project: "rr"}
	got := o.containerName("convex-backend")
	want := "cspace-rr-mercury-convex-backend"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
