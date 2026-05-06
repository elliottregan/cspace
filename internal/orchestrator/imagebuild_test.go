package orchestrator

import (
	"context"
	"testing"

	"github.com/elliottregan/cspace/internal/devcontainer"
)

func TestBuildProjectImageNoBuildNeeded(t *testing.T) {
	cases := []*devcontainer.Plan{
		nil,
		{Devcontainer: &devcontainer.Config{Image: "alpine"}},
		{Devcontainer: &devcontainer.Config{}},
	}
	for _, p := range cases {
		tag, err := BuildProjectImage(context.Background(), p)
		if err != nil {
			t.Fatalf("plan %+v: unexpected err %v", p, err)
		}
		if tag != "" {
			t.Fatalf("plan %+v: want empty tag, got %q", p, tag)
		}
	}
}

func TestProjectImageNameSanitization(t *testing.T) {
	cases := []struct{ in, want string }{
		{"my-project", "my-project"},
		{"My Project!", "my-project-"},
		{"", "unnamed"},
		{"foo_bar.baz", "foo_bar.baz"},
	}
	for _, c := range cases {
		if got := projectImageName(c.in); got != c.want {
			t.Errorf("projectImageName(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

func TestHashContextDeterministic(t *testing.T) {
	a := hashContext("/x", "Dockerfile")
	b := hashContext("/x", "Dockerfile")
	if a != b {
		t.Fatalf("non-deterministic: %s vs %s", a, b)
	}
	c := hashContext("/y", "Dockerfile")
	if a == c {
		t.Fatalf("different contexts produced same hash")
	}
}
