package corpus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFilter_SkipsBinary(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "image.png")
	if err := os.WriteFile(bin, []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x01, 0x02}, 0o644); err != nil {
		t.Fatal(err)
	}
	f := DefaultFilter()
	if f.Accept(bin) {
		t.Error("filter should reject binary file (contains null byte)")
	}
}

func TestFilter_SkipsOversized(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	// 300 KB of printable ASCII (no null bytes).
	buf := make([]byte, 300_000)
	for i := range buf {
		buf[i] = 'x'
	}
	if err := os.WriteFile(big, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	f := DefaultFilter()
	if f.Accept(big) {
		t.Error("filter should reject oversized file")
	}
}

func TestFilter_HonorsExcludeGlob(t *testing.T) {
	f := Filter{Excludes: []string{"vendor/**"}, MaxBytes: 1 << 30}
	if f.Accept("vendor/foo/bar.go") {
		t.Error("vendor/** should be rejected")
	}
	if f.Accept("vendor/foo.go") {
		t.Error("vendor/** should also match shallow vendor files")
	}
}

func TestFilter_AcceptsNormalTextFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(p, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := DefaultFilter()
	if !f.Accept(p) {
		t.Error("filter should accept normal Go source file")
	}
}

func TestFilter_HonorsLockAndSumGlobs(t *testing.T) {
	f := DefaultFilter()
	// These are glob matches on the filename only; need to still pass stat.
	// Simulate by using a path where the base matches and the file exists.
	dir := t.TempDir()
	for _, name := range []string{"go.sum", "package-lock.json"} {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte("{}"), 0o644)
		if f.Accept(p) {
			t.Errorf("%s should be rejected by default glob", name)
		}
	}
}
