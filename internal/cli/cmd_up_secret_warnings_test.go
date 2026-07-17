package cli

import (
	"reflect"
	"testing"
)

// TestEnvFileSecretCollisions covers the fail-loud warning check that fires
// when a project's compose env_file (.env / .env.cspace) silently overrides
// a cspace-delivered secret (ANTHROPIC_API_KEY, GH_TOKEN, ...). See
// docs/env-cspace.md's "Precedence (stated honestly)" section: env_file
// content wins over the delivered secret today, so this check only warns —
// it must never change which value ends up in the merged env.
func TestEnvFileSecretCollisions(t *testing.T) {
	secretKeys := []string{"ANTHROPIC_API_KEY", "GH_TOKEN"}

	cases := []struct {
		name         string
		secretValues map[string]string
		mergedEnv    map[string]string
		want         []string
	}{
		{
			name:         "overridden with a different value is reported",
			secretValues: map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api-real"},
			mergedEnv:    map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api-fake-from-envfile"},
			want:         []string{"ANTHROPIC_API_KEY"},
		},
		{
			name:         "blanked by env_file is reported",
			secretValues: map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api-real"},
			mergedEnv:    map[string]string{"ANTHROPIC_API_KEY": ""},
			want:         []string{"ANTHROPIC_API_KEY"},
		},
		{
			name:         "same value in env_file is not reported",
			secretValues: map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api-real"},
			mergedEnv:    map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api-real"},
			want:         nil,
		},
		{
			name:         "secret absent from secretValues is not reported",
			secretValues: map[string]string{},
			mergedEnv:    map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api-fake-from-envfile"},
			want:         nil,
		},
		{
			name:         "non-secret key differing is not reported",
			secretValues: map[string]string{"ANTHROPIC_API_KEY": "sk-ant-api-real"},
			mergedEnv: map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-api-real",
				"SOME_OTHER_VAR":    "different-value",
			},
			want: nil,
		},
		{
			name: "multiple collisions come back sorted",
			secretValues: map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-api-real",
				"GH_TOKEN":          "gh-real",
			},
			mergedEnv: map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-api-fake",
				"GH_TOKEN":          "gh-fake",
			},
			want: []string{"ANTHROPIC_API_KEY", "GH_TOKEN"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := envFileSecretCollisions(tc.secretValues, tc.mergedEnv, secretKeys)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("envFileSecretCollisions() = %#v, want %#v", got, tc.want)
			}
		})
	}
}
