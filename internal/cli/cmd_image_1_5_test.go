package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// Captured from `container image inspect cspace:compat15-brew` on 1.5.0
// after a Homebrew rc.52 image build on 2026-09-29. Only the nested version
// label was retained; image environment and history were not captured.
func TestParseImageInspectAppleContainer15(t *testing.T) {
	out, err := os.ReadFile(filepath.Join("testdata", "image-inspect-1.5.json"))
	if err != nil {
		t.Fatal(err)
	}
	version, hasLabel, present := parseImageInspect(out)
	if version != "1.0.0-rc.52" || !hasLabel || !present {
		t.Fatalf("parseImageInspect = (%q, %v, %v), want (1.0.0-rc.52, true, true)", version, hasLabel, present)
	}
}
