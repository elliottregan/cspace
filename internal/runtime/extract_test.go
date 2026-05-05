package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractCreatesLayout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	v, path, err := Extract("test-1.0.0")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if v != "test-1.0.0" {
		t.Fatalf("version=%q", v)
	}
	wantPath := filepath.Join(dir, ".cspace", "runtime", "test-1.0.0")
	if path != wantPath {
		t.Fatalf("path=%q want %q", path, wantPath)
	}
	if _, err := os.Stat(filepath.Join(path, "scripts", "cspace-entrypoint.sh")); err != nil {
		t.Fatalf("entrypoint script missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}

	// Scripts should be executable.
	info, err := os.Stat(filepath.Join(path, "scripts", "cspace-entrypoint.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("entrypoint not executable: mode=%v", info.Mode())
	}
}

func TestExtractIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if _, _, err := Extract("test-1.0.0"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Extract("test-1.0.0"); err != nil {
		t.Fatalf("second extract: %v", err)
	}
}

func TestExtractDifferentVersion(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if _, _, err := Extract("v1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Extract("v2"); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"v1", "v2"} {
		if _, err := os.Stat(filepath.Join(dir, ".cspace", "runtime", v, "manifest.json")); err != nil {
			t.Errorf("version %s extraction missing: %v", v, err)
		}
	}
}
