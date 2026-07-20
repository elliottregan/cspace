package cli

import (
	"strings"
	"testing"
)

func TestAttachArgs(t *testing.T) {
	bin, argv, err := attachArgs("cspace-demo-mercury")
	if err != nil {
		// container may not be on PATH in CI; only assert argv shape then.
		t.Skipf("container CLI not resolvable: %v", err)
	}
	if !strings.HasSuffix(bin, "container") {
		t.Errorf("bin = %q, want it to resolve the container binary", bin)
	}
	want := []string{"container", "exec", "-it", "cspace-demo-mercury", "claude", "--dangerously-skip-permissions"}
	if len(argv) != len(want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	for i := range want {
		if argv[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, argv[i], want[i])
		}
	}
}
