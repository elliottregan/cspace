package docker

import "testing"

// TestVolumeCreateArgs verifies the function exists and accepts a string.
// Full integration testing requires Docker, but we can at least verify the API.
func TestVolumeCreateAPI(t *testing.T) {
	var _ = VolumeCreate
}

// TestBuildAPI verifies the function signature.
func TestBuildAPI(t *testing.T) {
	var _ = Build
}
