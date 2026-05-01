package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBasic(t *testing.T) {
	got, err := parse(strings.NewReader("# comment\nKEY1=value1\nKEY2=value2\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]string{"KEY1": "value1", "KEY2": "value2"}
	if !equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseQuoted(t *testing.T) {
	in := strings.NewReader(`KEY1="double quoted"
KEY2='single quoted'
KEY3=unquoted
KEY4=  trimmed  `)
	got, err := parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := map[string]string{
		"KEY1": "double quoted",
		"KEY2": "single quoted",
		"KEY3": "unquoted",
		"KEY4": "trimmed",
	}
	for k, want := range cases {
		if got[k] != want {
			t.Errorf("%s: got %q, want %q", k, got[k], want)
		}
	}
}

func TestParseSkipsBlanksAndComments(t *testing.T) {
	in := strings.NewReader("\n# header comment\nKEY1=value1\n\n  # indented comment\nKEY2=value2\n")
	got, err := parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(got), got)
	}
}

func TestLoadReturnsEmptyForMissingFiles(t *testing.T) {
	dir := t.TempDir()
	got, err := loadFromDirs(dir, dir)
	if err != nil {
		t.Fatalf("loadFromDirs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %v", got)
	}
}

func TestLoadProjectOverridesGlobal(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()

	mustWriteSecret(t, filepath.Join(globalDir, ".cspace", "secrets.env"),
		"GLOBAL_ONLY=g\nSHARED=from-global\n")
	mustWriteSecret(t, filepath.Join(projectDir, ".cspace", "secrets.env"),
		"PROJECT_ONLY=p\nSHARED=from-project\n")

	got, err := loadFromDirs(globalDir, projectDir)
	if err != nil {
		t.Fatalf("loadFromDirs: %v", err)
	}
	if got["GLOBAL_ONLY"] != "g" {
		t.Errorf("GLOBAL_ONLY: got %q, want %q", got["GLOBAL_ONLY"], "g")
	}
	if got["PROJECT_ONLY"] != "p" {
		t.Errorf("PROJECT_ONLY: got %q, want %q", got["PROJECT_ONLY"], "p")
	}
	if got["SHARED"] != "from-project" {
		t.Errorf("SHARED: project should override global; got %q", got["SHARED"])
	}
}

func equal(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func mustWriteSecret(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
