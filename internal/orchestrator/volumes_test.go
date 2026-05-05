package orchestrator

import (
	"path/filepath"
	"strings"
	"testing"

	v2 "github.com/elliottregan/cspace/internal/compose/v2"
)

func TestResolveBindAbsolute(t *testing.T) {
	v := v2.Volume{Type: "bind", Source: "/host/data", Target: "/data"}
	got, err := resolveVolume(v, "p", "m", "/compose/dir", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.HostPath != "/host/data" || got.GuestPath != "/data" || got.ReadOnly {
		t.Fatalf("got %+v", got)
	}
}

func TestResolveBindRelative(t *testing.T) {
	v := v2.Volume{Type: "bind", Source: "./data", Target: "/data", ReadOnly: true}
	got, err := resolveVolume(v, "p", "m", "/compose/dir", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.HostPath != "/compose/dir/data" {
		t.Fatalf("hostpath=%s", got.HostPath)
	}
	if !got.ReadOnly {
		t.Fatal("readonly not preserved")
	}
}

func TestResolveNamedVolume(t *testing.T) {
	v := v2.Volume{Type: "volume", Source: "data", Target: "/data"}
	got, err := resolveVolume(v, "rr", "mercury", "/c", false, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(got.HostPath), "/.cspace/clones/rr/mercury/volumes/data") {
		t.Fatalf("hostpath=%s", got.HostPath)
	}
}

func TestResolveExternalVolume(t *testing.T) {
	v := v2.Volume{Type: "volume", Source: "shared", Target: "/d"}
	got, err := resolveVolume(v, "rr", "mercury", "/c", true, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.ToSlash(got.HostPath), "/.cspace/volumes/rr/shared") {
		t.Fatalf("hostpath=%s", got.HostPath)
	}
	if strings.Contains(filepath.ToSlash(got.HostPath), "/clones/") {
		t.Fatalf("external should not live under clones/: %s", got.HostPath)
	}
}

func TestResolveExternalWithExplicitName(t *testing.T) {
	v := v2.Volume{Type: "volume", Source: "shared", Target: "/d"}
	got, err := resolveVolume(v, "rr", "mercury", "/c", true, "global-shared")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(filepath.ToSlash(got.HostPath), "/.cspace/volumes/rr/global-shared") {
		t.Fatalf("hostpath=%s", got.HostPath)
	}
}

func TestResolveUnknownTypeErrors(t *testing.T) {
	v := v2.Volume{Type: "tmpfs", Target: "/t"}
	_, err := resolveVolume(v, "rr", "m", "/c", false, "")
	if err == nil {
		t.Fatal("want error for tmpfs, got nil")
	}
}
