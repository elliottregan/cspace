package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestList(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	for _, v := range []string{"0.9.0", "1.0.0-rc.1", "1.0.0"} {
		_ = os.MkdirAll(filepath.Join(dir, ".cspace", "runtime", v), 0o755)
	}
	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d, want 3", len(got))
	}
}

func TestListEmptyWhenMissing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	got, err := List()
	if err != nil {
		t.Fatalf("List on missing root: %v", err)
	}
	if got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

func TestPruneKeepsActiveAndLatestN(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	for _, v := range []string{"0.9.0", "1.0.0-rc.1", "1.0.0", "1.0.1"} {
		_ = os.MkdirAll(filepath.Join(dir, ".cspace", "runtime", v), 0o755)
	}
	if err := Prune("1.0.1", 1); err != nil {
		t.Fatal(err)
	}
	versions, _ := List()
	if len(versions) != 2 {
		t.Fatalf("after prune: got %d, want 2 (active + 1 previous)", len(versions))
	}
	// Should keep 1.0.1 (active) and 1.0.0 (latest non-active).
	wantSet := map[string]bool{"1.0.1": true, "1.0.0": true}
	for _, v := range versions {
		if !wantSet[v] {
			t.Errorf("unexpected version retained: %s", v)
		}
	}
}

func TestPruneKeepsActiveOnlyWhenZero(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	for _, v := range []string{"0.9.0", "1.0.0", "1.0.1"} {
		_ = os.MkdirAll(filepath.Join(dir, ".cspace", "runtime", v), 0o755)
	}
	if err := Prune("1.0.1", 0); err != nil {
		t.Fatal(err)
	}
	versions, _ := List()
	if len(versions) != 1 || versions[0] != "1.0.1" {
		t.Fatalf("want only [1.0.1], got %v", versions)
	}
}
