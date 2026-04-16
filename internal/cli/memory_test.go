package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// The stub used by provision.ensureMemoryDir. Duplicated here intentionally
// (see isStubMemory's comment) — if the stub text ever changes, both need to
// track, and this test enforces that.
const stubMemoryForTest = `<!--
This directory holds project-shared Claude Code memory.

It is bind-mounted into every cspace container at:
  /home/dev/.claude/projects/-workspace/memory
-->
`

func TestNonStubEntries_MissingDir(t *testing.T) {
	root := t.TempDir()
	entries, err := nonStubEntries(filepath.Join(root, "does-not-exist"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty, got %v", entries)
	}
}

func TestNonStubEntries_EmptyDir(t *testing.T) {
	root := t.TempDir()
	entries, err := nonStubEntries(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty, got %v", entries)
	}
}

func TestNonStubEntries_OnlyStub(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte(stubMemoryForTest), 0644); err != nil {
		t.Fatal(err)
	}
	entries, err := nonStubEntries(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected empty (stub should be ignored), got %v", entries)
	}
}

func TestNonStubEntries_StubPlusRealContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte(stubMemoryForTest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "user_role.md"), []byte("real memory"), 0644); err != nil {
		t.Fatal(err)
	}
	entries, err := nonStubEntries(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0] != "user_role.md" {
		t.Errorf("expected [user_role.md], got %v", entries)
	}
}

func TestNonStubEntries_EditedMemoryCountsAsRealContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "MEMORY.md"), []byte("- [user_role](user_role.md) — the user is a data scientist\n"), 0644); err != nil {
		t.Fatal(err)
	}
	entries, err := nonStubEntries(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0] != "MEMORY.md" {
		t.Errorf("expected [MEMORY.md] (edited stub is user content), got %v", entries)
	}
}

func TestIsStubMemory(t *testing.T) {
	root := t.TempDir()
	stubPath := filepath.Join(root, "stub.md")
	editedPath := filepath.Join(root, "edited.md")
	missingPath := filepath.Join(root, "absent.md")

	if err := os.WriteFile(stubPath, []byte(stubMemoryForTest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(editedPath, []byte("# My Memory\n\n- preferences...\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if !isStubMemory(stubPath) {
		t.Error("stub should match isStubMemory")
	}
	if isStubMemory(editedPath) {
		t.Error("edited content should not match isStubMemory")
	}
	if isStubMemory(missingPath) {
		t.Error("missing file should not match isStubMemory (must return false, not error)")
	}
}
