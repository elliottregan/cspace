package corpus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitInit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
}

func TestCodeCorpus_EnumeratesGitTrackedTextFiles(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "image.png"), []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("add", "-A")
	runGit("commit", "-qm", "init")

	cc := &CodeCorpus{Filter: DefaultFilter(), Chunk: ChunkConfig{Max: 12000, Overlap: 200}}
	recs, errs := cc.Enumerate(dir)
	go func() {
		for range errs {
		}
	}()
	var paths []string
	for r := range recs {
		paths = append(paths, r.Path)
		if r.ContentHash == "" {
			t.Errorf("record missing ContentHash: %+v", r)
		}
		if r.EmbedText == "" {
			t.Errorf("record missing EmbedText: %+v", r)
		}
	}
	if len(paths) != 1 || paths[0] != "hello.go" {
		t.Errorf("expected only hello.go, got %v", paths)
	}
}

func TestCodeCorpus_ID(t *testing.T) {
	cc := &CodeCorpus{}
	if cc.ID() != "code" {
		t.Errorf("expected ID code, got %q", cc.ID())
	}
}

func TestCodeCorpus_CollectionName(t *testing.T) {
	cc := &CodeCorpus{}
	n := cc.Collection(".")
	if !strings.HasPrefix(n, "code-") {
		t.Errorf("expected code- prefix, got %q", n)
	}
}

func TestCodeCorpus_ChunksLargeFile(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	// Build a 20k-char file so it exceeds the 12 000 char threshold.
	big := strings.Repeat("x", 20000)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("add", "-A")
	runGit("commit", "-qm", "init")

	cc := &CodeCorpus{
		Filter: Filter{MaxBytes: 1 << 30}, // no size cap, no excludes
		Chunk:  ChunkConfig{Max: 8000, Overlap: 200},
	}
	recs, errs := cc.Enumerate(dir)
	go func() {
		for range errs {
		}
	}()
	var count int
	for r := range recs {
		count++
		if r.Kind != "chunk" {
			t.Errorf("expected Kind=chunk (file is oversized), got %q", r.Kind)
		}
	}
	if count < 2 {
		t.Errorf("expected ≥2 chunk records, got %d", count)
	}
}
