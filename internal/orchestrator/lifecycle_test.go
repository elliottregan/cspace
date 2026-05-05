package orchestrator

import (
	"context"
	"fmt"
	"strings"
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

func TestSpawnOrderHonorsDependsOn(t *testing.T) {
	stub := newStub()
	orch := &Orchestration{
		Sandbox: "m", Project: "p",
		Plan: &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{},
			Compose: &v2.Project{
				Services: map[string]*v2.Service{
					"backend": {Name: "backend", Image: "be"},
					"dashboard": {
						Name:      "dashboard",
						Image:     "dash",
						DependsOn: []v2.Dependency{{Name: "backend", Condition: "service_started"}},
					},
				},
			},
		},
		Substrate: stub,
	}
	if err := orch.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(stub.runs) != 2 {
		t.Fatalf("expected 2 spawns, got %d", len(stub.runs))
	}
	if stub.runs[0].Name != orch.containerName("backend") {
		t.Fatalf("backend should spawn first; got runs[0]=%q", stub.runs[0].Name)
	}
	if stub.runs[1].Name != orch.containerName("dashboard") {
		t.Fatalf("dashboard should spawn second; got runs[1]=%q", stub.runs[1].Name)
	}
}

func TestCycleDetected(t *testing.T) {
	stub := newStub()
	orch := &Orchestration{
		Sandbox: "m", Project: "p",
		Plan: &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{},
			Compose: &v2.Project{
				Services: map[string]*v2.Service{
					"a": {Name: "a", DependsOn: []v2.Dependency{{Name: "b"}}},
					"b": {Name: "b", DependsOn: []v2.Dependency{{Name: "a"}}},
				},
			},
		},
		Substrate: stub,
	}
	err := orch.Up(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestUpResolvesNamedVolume(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	stub := newStub()
	orch := &Orchestration{
		Sandbox: "m", Project: "p",
		Plan: &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{},
			Compose: &v2.Project{
				SourcePath: "/tmp/compose.yml",
				Services: map[string]*v2.Service{
					"db": {
						Name:  "db",
						Image: "postgres",
						Volumes: []v2.Volume{
							{Type: "volume", Source: "pgdata", Target: "/var/lib/postgresql"},
						},
					},
				},
				NamedVolumes: map[string]*v2.NamedVolume{
					"pgdata": {External: false},
				},
			},
		},
		Substrate: stub,
	}
	if err := orch.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(stub.runs) != 1 {
		t.Fatalf("runs=%d", len(stub.runs))
	}
	if len(stub.runs[0].Volumes) != 1 {
		t.Fatalf("expected 1 volume, got %d", len(stub.runs[0].Volumes))
	}
	if stub.runs[0].Volumes[0].GuestPath != "/var/lib/postgresql" {
		t.Fatalf("guest=%q", stub.runs[0].Volumes[0].GuestPath)
	}
}
