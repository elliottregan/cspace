package devcontainer

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeCatchesMissingService(t *testing.T) {
	// with_compose.json declares service "app" but compose file (which we
	// don't yet provide alongside) is "docker-compose.yml" — Load doesn't
	// resolve the compose ref. Merge will try and fail. To make the test
	// deterministic, use a config object directly.
	c := &Config{
		Service:           "no-such-service",
		DockerComposeFile: StringOrSlice{"merge_compose.yml"},
		SourcePath:        "testdata/dummy.json",
	}
	_, err := Merge(c, "testdata")
	if err == nil || !strings.Contains(err.Error(), "service") {
		t.Fatalf("want missing-service error, got %v", err)
	}
}

func TestMergeNoComposeOK(t *testing.T) {
	c := &Config{Image: "alpine"}
	plan, err := Merge(c, ".")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if plan.Compose != nil {
		t.Fatalf("compose should be nil when no dockerComposeFile")
	}
}

func TestMergeWithComposeFile(t *testing.T) {
	// We'll create a tiny compose fixture for this test.
	c := &Config{
		Service:           "app",
		DockerComposeFile: StringOrSlice{"merge_compose.yml"},
		SourcePath:        filepath.Join("testdata", "dummy.json"),
	}
	plan, err := Merge(c, "testdata")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if plan.Compose == nil {
		t.Fatal("compose nil after successful merge")
	}
	if plan.Service != "app" {
		t.Fatalf("plan.Service=%q", plan.Service)
	}
}

func TestMergeBadExtractCredentialsRef(t *testing.T) {
	c := &Config{
		Service:           "app",
		DockerComposeFile: StringOrSlice{"merge_compose.yml"},
		SourcePath:        filepath.Join("testdata", "dummy.json"),
		Customizations: Customizations{
			Cspace: CspaceCustomizations{
				ExtractCredentials: []ExtractCredential{
					{From: "ghost-service", Exec: []string{"x"}, Env: "FOO"},
				},
			},
		},
	}
	_, err := Merge(c, "testdata")
	if err == nil || !strings.Contains(err.Error(), "ghost-service") {
		t.Fatalf("want unknown-service error, got %v", err)
	}
}
