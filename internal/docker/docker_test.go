package docker

import "testing"

// TestVolumeCreateArgs verifies the function exists and accepts a string.
// Full integration testing requires Docker, but we can at least verify the API.
func TestVolumeCreateAPI(t *testing.T) {
	// Just verify the function signature compiles correctly
	var _ func(string) error = VolumeCreate
}

// TestBuildAPI verifies the function signature.
func TestBuildAPI(t *testing.T) {
	var _ func(string, string, string, bool) error = Build
}
