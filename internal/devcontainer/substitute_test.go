package devcontainer

import "testing"

func TestSubstituteLocalEnv(t *testing.T) {
	t.Setenv("CSPACE_TEST_FOO", "foo-value")
	t.Setenv("CSPACE_TEST_EMPTY", "")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no substitution", "plain", "plain"},
		{"single var present", "${localEnv:CSPACE_TEST_FOO}", "foo-value"},
		{"single var missing", "${localEnv:CSPACE_TEST_MISSING}", ""},
		{"missing var with default", "${localEnv:CSPACE_TEST_MISSING:fallback}", "fallback"},
		{"present var ignores default", "${localEnv:CSPACE_TEST_FOO:fallback}", "foo-value"},
		{"empty var stays empty (set, not unset)", "${localEnv:CSPACE_TEST_EMPTY:fallback}", ""},
		{"two vars in one string", "${localEnv:CSPACE_TEST_FOO}-${localEnv:CSPACE_TEST_MISSING:def}", "foo-value-def"},
		{"surrounding text preserved", "prefix-${localEnv:CSPACE_TEST_FOO}-suffix", "prefix-foo-value-suffix"},
		{"unrelated brace pattern untouched", "${not_localEnv:VAR}", "${not_localEnv:VAR}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := substituteLocalEnv(c.in)
			if got != c.want {
				t.Fatalf("substituteLocalEnv(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestResolveLocalEnvOnConfig(t *testing.T) {
	t.Setenv("CSPACE_TEST_KEY", "secret-123")
	cfg := &Config{
		ContainerEnv: map[string]string{
			"DIRECT":    "literal",
			"FROM_HOST": "${localEnv:CSPACE_TEST_KEY}",
			"WITH_DEF":  "${localEnv:CSPACE_TEST_NOPE:fallback}",
		},
		PostCreateCommand: StringOrSlice{"echo ${localEnv:CSPACE_TEST_KEY}"},
		PostStartCommand:  StringOrSlice{"true"},
	}
	cfg.resolveLocalEnv()

	if cfg.ContainerEnv["DIRECT"] != "literal" {
		t.Fatalf("DIRECT mutated: %q", cfg.ContainerEnv["DIRECT"])
	}
	if cfg.ContainerEnv["FROM_HOST"] != "secret-123" {
		t.Fatalf("FROM_HOST = %q", cfg.ContainerEnv["FROM_HOST"])
	}
	if cfg.ContainerEnv["WITH_DEF"] != "fallback" {
		t.Fatalf("WITH_DEF = %q", cfg.ContainerEnv["WITH_DEF"])
	}
	if cfg.PostCreateCommand[0] != "echo secret-123" {
		t.Fatalf("postCreate = %q", cfg.PostCreateCommand[0])
	}
}
