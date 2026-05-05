package runtime

import (
	"os"
	"path/filepath"
	"sort"

	"github.com/Masterminds/semver/v3"
)

func runtimeRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cspace", "runtime"), nil
}

// List returns the names of installed runtime overlay versions, sorted
// using semantic versioning. Returns nil (without error) when the runtime
// root doesn't yet exist.
func List() ([]string, error) {
	root, err := runtimeRoot()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	// Sort using semantic versioning; fall back to lexicographic for non-semver names
	sort.Slice(out, func(i, j int) bool {
		vi, erri := semver.NewVersion(out[i])
		vj, errj := semver.NewVersion(out[j])
		if erri == nil && errj == nil {
			return vi.LessThan(vj)
		}
		return out[i] < out[j]
	})
	return out, nil
}

// Prune removes overlay versions other than the active one and the
// most recent keepPrevious by sort order. Active is always retained.
// keepPrevious=0 retains only the active version.
func Prune(active string, keepPrevious int) error {
	versions, err := List()
	if err != nil {
		return err
	}
	root, _ := runtimeRoot()
	keep := map[string]bool{active: true}
	count := 0
	for i := len(versions) - 1; i >= 0 && count < keepPrevious; i-- {
		if versions[i] != active {
			keep[versions[i]] = true
			count++
		}
	}
	for _, v := range versions {
		if keep[v] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, v)); err != nil {
			return err
		}
	}
	return nil
}
