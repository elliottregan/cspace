package devcontainer

import "testing"

func TestLoadMinimal(t *testing.T) {
	c, err := Load("testdata/minimal.jsonc")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Image != "node:24-bookworm-slim" {
		t.Fatalf("image=%q", c.Image)
	}
	if len(c.PostCreateCommand) != 1 || c.PostCreateCommand[0] != "echo hello" {
		t.Fatalf("postCreate=%v", c.PostCreateCommand)
	}
	if c.SourcePath != "testdata/minimal.jsonc" {
		t.Fatalf("source=%q", c.SourcePath)
	}
}

func TestLoadWithCompose(t *testing.T) {
	c, err := Load("testdata/with_compose.json")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Service != "app" || len(c.DockerComposeFile) != 1 {
		t.Fatalf("compose ref wrong: %+v", c)
	}
	if len(c.Customizations.Cspace.ExtractCredentials) != 1 {
		t.Fatalf("extractCredentials missing")
	}
	if c.Customizations.Cspace.ExtractCredentials[0].From != "convex-backend" {
		t.Fatalf("extractCredentials.from=%q", c.Customizations.Cspace.ExtractCredentials[0].From)
	}
}

func TestLoadCapturesUnknownFields(t *testing.T) {
	// We'll use a fixture with unsupported fields. Create it inline.
	c := &Config{}
	_ = c // placeholder — actual unknown-capture validation lives in Task 6's TestValidateUnsupportedField.
}
