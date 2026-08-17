package cli

import "testing"

func TestValidateProjectScopeRefusesDerivedName(t *testing.T) {
	// projectName() falls back to the directory basename and then to the
	// literal "default". Writing cspace-default-<KEY> would create an entry
	// silently shared by every unnamed project on the host — the opposite of
	// the least privilege --project exists to provide.
	cases := []struct {
		name     string
		explicit bool
	}{
		{"", false},
		{"resume-redux", false}, // derived from the directory basename
		{"default", true},
		{"", true},
	}
	for _, tc := range cases {
		if err := validateProjectScope(tc.name, tc.explicit); err == nil {
			t.Errorf("validateProjectScope(%q, %v) = nil, want an error", tc.name, tc.explicit)
		}
	}
}

func TestValidateProjectScopeAcceptsExplicitName(t *testing.T) {
	if err := validateProjectScope("resume-redux", true); err != nil {
		t.Fatalf("validateProjectScope() = %v, want nil", err)
	}
}

func TestKeychainServiceScoping(t *testing.T) {
	if got := keychainService("", "GH_TOKEN"); got != "cspace-GH_TOKEN" {
		t.Errorf("global service = %q, want cspace-GH_TOKEN", got)
	}
	if got := keychainService("resume-redux", "GH_TOKEN"); got != "cspace-resume-redux-GH_TOKEN" {
		t.Errorf("scoped service = %q, want cspace-resume-redux-GH_TOKEN", got)
	}
}
