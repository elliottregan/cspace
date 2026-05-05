package devcontainer

import (
	"strings"
	"testing"
)

func TestValidateUnsupportedField(t *testing.T) {
	c, err := Load("testdata/with_unsupported.jsonc")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	err = c.Validate()
	if err == nil {
		t.Fatal("want error, got nil")
	}
	for _, want := range []string{"runArgs", "shutdownAction"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing field %q: %v", want, err)
		}
	}
}

func TestValidateServiceWithoutCompose(t *testing.T) {
	c := &Config{Service: "app"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "dockerComposeFile") {
		t.Fatalf("want missing-dockerComposeFile error, got %v", err)
	}
}

func TestValidateImageVsDockerFileExclusive(t *testing.T) {
	c := &Config{Image: "alpine", DockerFile: "Dockerfile"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("want exclusion error, got %v", err)
	}
}

func TestValidateBuildVsDockerFileExclusive(t *testing.T) {
	c := &Config{Build: &BuildConfig{Context: "."}, DockerFile: "Dockerfile"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("want exclusion error, got %v", err)
	}
}

func TestValidateExtractCredentialsRequired(t *testing.T) {
	c := &Config{
		Customizations: Customizations{
			Cspace: CspaceCustomizations{
				ExtractCredentials: []ExtractCredential{{From: "be"}}, // missing exec, env
			},
		},
	}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "extractCredentials") {
		t.Fatalf("want extractCredentials error, got %v", err)
	}
}

func TestValidateMinimalOK(t *testing.T) {
	c, err := Load("testdata/minimal.jsonc")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}
