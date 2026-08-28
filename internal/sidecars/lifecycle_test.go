package sidecars

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
	execs map[string][][]string // container → list of cmds
	ips   map[string]string
}

func newStub() *stubSubstrate {
	return &stubSubstrate{
		execs: map[string][][]string{},
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
	s.execs[name] = append(s.execs[name], cmd)
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
	stub.ips["cspace-rr-mercury"] = "192.168.64.40" // sandbox
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
		Plan:      &devcontainer.Plan{Devcontainer: &devcontainer.Config{Image: "alpine"}},
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
	// Non-external named volumes resolve to substrate-managed ext4
	// volumes (NamedVolumes), not host bind mounts (Volumes). This is
	// the v0-Docker-Desktop equivalent: in-VM filesystem, no virtio-fs.
	if len(stub.runs[0].NamedVolumes) != 1 {
		t.Fatalf("expected 1 named volume, got %d", len(stub.runs[0].NamedVolumes))
	}
	if stub.runs[0].NamedVolumes[0].GuestPath != "/var/lib/postgresql" {
		t.Fatalf("guest=%q", stub.runs[0].NamedVolumes[0].GuestPath)
	}
	if stub.runs[0].NamedVolumes[0].Name != "cspace-p-m-pgdata" {
		t.Fatalf("name=%q", stub.runs[0].NamedVolumes[0].Name)
	}
	if len(stub.runs[0].Volumes) != 0 {
		t.Fatalf("expected no bind mounts, got %d", len(stub.runs[0].Volumes))
	}
}

func TestUpInjectsHostsIntoAllMicrovms(t *testing.T) {
	stub := newStub()
	stub.ips["cspace-rr-mercury"] = "192.168.64.40" // sandbox
	orch := &Orchestration{
		Sandbox: "mercury", Project: "rr",
		Plan: &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{},
			Compose: &v2.Project{
				SourcePath: "/tmp/compose.yml",
				Services: map[string]*v2.Service{
					"app":     {Name: "app", Image: "alpine"},
					"backend": {Name: "backend", Image: "be"},
				},
			},
			Service: "app",
		},
		Substrate: stub,
	}
	if err := orch.Up(context.Background()); err != nil {
		t.Fatal(err)
	}
	// We should have execs against both the sandbox container
	// "cspace-rr-mercury" and the sidecar "cspace-rr-mercury-backend".
	for _, name := range []string{"cspace-rr-mercury", orch.containerName("backend")} {
		if _, ok := stub.execs[name]; !ok {
			t.Errorf("no exec recorded for %s; got %v", name, stub.execs)
		}
	}
}

func TestDownStopsAllSidecars(t *testing.T) {
	stub := newStub()
	orch := &Orchestration{
		Sandbox: "mercury", Project: "rr",
		Plan: &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{Service: "app"},
			Compose: &v2.Project{
				Services: map[string]*v2.Service{
					"app":     {Name: "app", Image: "alpine"},
					"backend": {Name: "backend", Image: "be"},
					"dash":    {Name: "dash", Image: "dash"},
				},
			},
			Service: "app",
		},
		Substrate: stub,
	}
	if err := orch.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(stub.stops) != 2 {
		t.Fatalf("expected 2 stops (no app), got %v", stub.stops)
	}
	for _, s := range stub.stops {
		if s == orch.containerName("app") {
			t.Fatalf("app should not be stopped by orchestrator: %v", stub.stops)
		}
	}
}

func TestDownNoComposeIsNoOp(t *testing.T) {
	stub := newStub()
	orch := &Orchestration{
		Plan:      &devcontainer.Plan{Devcontainer: &devcontainer.Config{}},
		Substrate: stub,
	}
	if err := orch.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(stub.stops) != 0 {
		t.Fatalf("expected no stops, got %v", stub.stops)
	}
}

// TestSandboxHostsOmitsSidecarIPs — the workspace resolves sidecars through
// daemon DNS, which re-inspects the container per query. An /etc/hosts entry
// would defeat that: files are consulted before DNS, so a boot-time IP would
// win over the correct answer for the life of the sandbox. That is the bug
// behind the 2026-08-28 convex-backend incident, where the recovery was
// hand-editing this file.
func TestSandboxHostsOmitsSidecarIPs(t *testing.T) {
	stub := newStub()
	stub.ips["cspace-rr-mercury"] = "192.168.64.40"
	orch := &Orchestration{
		Sandbox: "mercury", Project: "rr",
		Plan: &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{},
			Compose: &v2.Project{
				SourcePath: "/tmp/compose.yml",
				Services: map[string]*v2.Service{
					"app":     {Name: "app", Image: "alpine"},
					"backend": {Name: "backend", Image: "be"},
				},
			},
			Service: "app",
		},
		Substrate: stub,
	}
	if err := orch.Up(context.Background()); err != nil {
		t.Fatal(err)
	}

	sandboxHosts := hostsContentFor(t, stub, "cspace-rr-mercury")
	if strings.Contains(sandboxHosts, "backend") {
		t.Errorf("sandbox /etc/hosts pins the sidecar IP; DNS can never correct it:\n%s", sandboxHosts)
	}
	// Its own aliases stay: `app` is the sandbox itself (an IP that cannot
	// drift without the sandbox being recreated), and `workspace` is the
	// stable loopback name scripts use.
	if !strings.Contains(sandboxHosts, "app") || !strings.Contains(sandboxHosts, "workspace") {
		t.Errorf("sandbox lost its own aliases:\n%s", sandboxHosts)
	}
}

// TestSidecarHostsKeepsFullMap — sidecars run their own images with no cspace
// entrypoint, so they have no dnsmasq translating :53 to the daemon's
// gateway:5354, and glibc cannot express a non-standard port in resolv.conf.
// /etc/hosts is the only addressing they have; removing it would strand
// sidecar-to-sidecar traffic with nothing to fall back on.
func TestSidecarHostsKeepsFullMap(t *testing.T) {
	stub := newStub()
	stub.ips["cspace-rr-mercury"] = "192.168.64.40"
	orch := &Orchestration{
		Sandbox: "mercury", Project: "rr",
		Plan: &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{},
			Compose: &v2.Project{
				SourcePath: "/tmp/compose.yml",
				Services: map[string]*v2.Service{
					"app":       {Name: "app", Image: "alpine"},
					"backend":   {Name: "backend", Image: "be"},
					"dashboard": {Name: "dashboard", Image: "db"},
				},
			},
			Service: "app",
		},
		Substrate: stub,
	}
	if err := orch.Up(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := hostsContentFor(t, stub, orch.containerName("dashboard"))
	for _, peer := range []string{"backend", "app"} {
		if !strings.Contains(got, peer) {
			t.Errorf("sidecar /etc/hosts lost peer %q, its only way to address it:\n%s", peer, got)
		}
	}
}

// hostsContentFor digs the injected /etc/hosts payload out of the recorded
// execs for a container.
func hostsContentFor(t *testing.T, stub *stubSubstrate, container string) string {
	t.Helper()
	cmds, ok := stub.execs[container]
	if !ok {
		t.Fatalf("no exec recorded for %s; got %v", container, stub.execs)
	}
	for _, cmd := range cmds {
		last := cmd[len(cmd)-1]
		if strings.Contains(last, ">> /etc/hosts") {
			return last
		}
	}
	t.Fatalf("no /etc/hosts injection recorded for %s: %v", container, cmds)
	return ""
}
