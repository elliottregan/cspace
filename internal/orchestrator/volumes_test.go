package orchestrator

import (
	"path/filepath"
	"strings"
	"testing"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

func TestResolveBindAbsolute(t *testing.T) {
	v := v2.Volume{Type: "bind", Source: "/host/data", Target: "/data"}
	got, err := ResolveVolume(v, "p", "m", "/compose/dir", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Bind == nil || got.Named != nil {
		t.Fatalf("bind expected, got %+v", got)
	}
	if got.Bind.HostPath != "/host/data" || got.Bind.GuestPath != "/data" || got.Bind.ReadOnly {
		t.Fatalf("got %+v", got.Bind)
	}
}

func TestResolveBindRelative(t *testing.T) {
	v := v2.Volume{Type: "bind", Source: "./data", Target: "/data", ReadOnly: true}
	got, err := ResolveVolume(v, "p", "m", "/compose/dir", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Bind == nil {
		t.Fatalf("bind expected, got %+v", got)
	}
	if got.Bind.HostPath != "/compose/dir/data" {
		t.Fatalf("hostpath=%s", got.Bind.HostPath)
	}
	if !got.Bind.ReadOnly {
		t.Fatal("readonly not preserved")
	}
}

// Non-external named volumes resolve to substrate-managed ext4 volumes.
// The name is scoped cspace-<project>-<sandbox>-<source> so per-sandbox
// volumes can't collide and cspace down can prune cleanly.
func TestResolveNamedVolume(t *testing.T) {
	v := v2.Volume{Type: "volume", Source: "data", Target: "/data"}
	got, err := ResolveVolume(v, "rr", "mercury", "/c", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Named == nil || got.Bind != nil {
		t.Fatalf("named volume expected, got %+v", got)
	}
	if got.Named.Name != "cspace-rr-mercury-data" {
		t.Fatalf("name=%s", got.Named.Name)
	}
	if got.Named.GuestPath != "/data" {
		t.Fatalf("guestpath=%s", got.Named.GuestPath)
	}
}

// External named volumes still resolve to a host bind under
// ~/.cspace/volumes/<project>/<name>/ — Apple Container volumes are
// exclusive (one container at a time), so cross-sandbox sharing has to
// stay on the virtio-fs path.
func TestResolveExternalVolume(t *testing.T) {
	v := v2.Volume{Type: "volume", Source: "shared", Target: "/d"}
	got, err := ResolveVolume(v, "rr", "mercury", "/c", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Bind == nil || got.Named != nil {
		t.Fatalf("external expected as bind, got %+v", got)
	}
	if !strings.Contains(filepath.ToSlash(got.Bind.HostPath), "/.cspace/volumes/rr/shared") {
		t.Fatalf("hostpath=%s", got.Bind.HostPath)
	}
	if strings.Contains(filepath.ToSlash(got.Bind.HostPath), "/clones/") {
		t.Fatalf("external should not live under clones/: %s", got.Bind.HostPath)
	}
}

func TestResolveExternalWithExplicitName(t *testing.T) {
	v := v2.Volume{Type: "volume", Source: "shared", Target: "/d"}
	got, err := ResolveVolume(v, "rr", "mercury", "/c", true, "global-shared")
	if err != nil {
		t.Fatal(err)
	}
	if got.Bind == nil {
		t.Fatalf("bind expected, got %+v", got)
	}
	if !strings.HasSuffix(filepath.ToSlash(got.Bind.HostPath), "/.cspace/volumes/rr/global-shared") {
		t.Fatalf("hostpath=%s", got.Bind.HostPath)
	}
}

func TestResolveUnknownTypeErrors(t *testing.T) {
	v := v2.Volume{Type: "tmpfs", Target: "/t"}
	_, err := ResolveVolume(v, "rr", "m", "/c", false, "")
	if err == nil {
		t.Fatal("want error for tmpfs, got nil")
	}
}
