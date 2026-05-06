package orchestrator

import (
	"context"
	"testing"

	"github.com/elliottregan/cspace/internal/devcontainer"
)

// cannedSub returns a fixed stdout regardless of args.
type cannedSub struct {
	stubSubstrate
	canned map[string]string // container name → stdout
}

func newCannedSub() *cannedSub {
	return &cannedSub{
		stubSubstrate: stubSubstrate{
			execs: map[string][][]string{},
			ips:   map[string]string{},
		},
		canned: map[string]string{},
	}
}

func (c *cannedSub) Exec(_ context.Context, name string, cmd []string) (string, error) {
	c.execs[name] = append(c.execs[name], cmd)
	return c.canned[name], nil
}

func TestExtractCredentialsTrimsByDefault(t *testing.T) {
	sub := newCannedSub()
	sub.canned["cspace-p-m-backend"] = "key-xyz\n"
	ec := devcontainer.ExtractCredential{
		From: "backend", Exec: []string{"./gen.sh"}, Env: "ADMIN_KEY",
	}
	got, err := extractOne(context.Background(), sub, "cspace-p-m-backend", ec, true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "key-xyz" {
		t.Fatalf("got %q (trim should strip trailing whitespace)", got)
	}
}

func TestExtractCredentialsNoTrimWhenDisabled(t *testing.T) {
	sub := newCannedSub()
	sub.canned["cspace-p-m-backend"] = "value\n"
	ec := devcontainer.ExtractCredential{From: "backend", Exec: []string{"x"}, Env: "K"}
	got, err := extractOne(context.Background(), sub, "cspace-p-m-backend", ec, false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "value\n" {
		t.Fatalf("got %q (should preserve newline)", got)
	}
}

func TestExtractAllSetsExtractedEnv(t *testing.T) {
	sub := newCannedSub()
	sub.canned["cspace-p-m-backend"] = "secret-key\n"
	orch := &Orchestration{
		Sandbox: "m", Project: "p", Substrate: sub,
		Plan: &devcontainer.Plan{
			Devcontainer: &devcontainer.Config{
				Customizations: devcontainer.Customizations{
					Cspace: devcontainer.CspaceCustomizations{
						ExtractCredentials: []devcontainer.ExtractCredential{
							{From: "backend", Exec: []string{"./gen.sh"}, Env: "ADMIN_KEY"},
						},
					},
				},
			},
		},
	}
	if err := orch.ExtractAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if orch.ExtractedEnv["ADMIN_KEY"] != "secret-key" {
		t.Fatalf("ExtractedEnv[ADMIN_KEY]=%q", orch.ExtractedEnv["ADMIN_KEY"])
	}
}
