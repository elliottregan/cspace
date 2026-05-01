//go:build !darwin

package secrets

import "time"

// On non-darwin platforms, Keychain integration is not implemented.
// Future: secret-tool (Linux GNOME Keyring), libsecret, or per-platform.

func ReadKeychain(_ string) (string, error)                 { return "", nil }
func WriteKeychain(_, _ string) error                       { return nil }
func DiscoverClaudeOauthToken() (string, time.Time, error)  { return "", time.Time{}, nil }
