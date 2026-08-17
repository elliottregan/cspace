package credentials

import (
	"os"

	"github.com/elliottregan/cspace/internal/secrets"
)

// ProductionHost wires the real host dependencies: the macOS Keychain,
// dotenv secrets files, ambient shell env, host credential discovery, and
// GitHub's /user endpoint.
//
// Every field is a seam, so tests construct a Host with stubs instead and
// never touch real credential stores or the network.
func ProductionHost() Host {
	return Host{
		ReadKeychain:    secrets.ReadKeychain,
		ReadSecretsFile: secrets.ParseFile,
		LookupEnv:       os.LookupEnv,
		DiscoverClaude:  secrets.DiscoverClaudeOauthToken,
		DiscoverGh:      secrets.DiscoverGhAuthToken,
		VerifyGitHub:    liveVerifyGitHub,
	}
}

// EnvFlagCredentials extracts the cspace-owned keys from `--env KEY=VALUE`
// arguments so they enter resolution as SourceEnvFlag — the highest rank —
// rather than being stripped as project env by Bake.
//
// Non-credential --env values are left for the caller to merge into the app
// environment as before.
func EnvFlagCredentials(pairs []string) map[string]string {
	out := map[string]string{}
	for _, kv := range pairs {
		k, v, found := cut(kv)
		if !found || !IsOwnedKey(k) {
			continue
		}
		out[k] = v
	}
	return out
}

func cut(kv string) (key, value string, found bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			if i == 0 {
				return "", "", false
			}
			return kv[:i], kv[i+1:], true
		}
	}
	return "", "", false
}
