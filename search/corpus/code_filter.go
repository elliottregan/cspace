package corpus

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Filter decides whether a file should be indexed.
type Filter struct {
	MaxBytes int64
	Excludes []string // glob patterns, project-relative or base-name
}

// DefaultFilter returns sane defaults for CodeCorpus.
func DefaultFilter() Filter {
	return Filter{
		MaxBytes: 200 * 1024,
		Excludes: []string{
			"vendor/**",
			"internal/assets/embedded/**",
			"docs/superpowers/specs/**",
			"*.lock",
			"*.sum",
			"package-lock.json",
			"*.png", "*.jpg", "*.gif", "*.ico", "*.pdf",
			"*.zip", "*.tar.gz",
		},
	}
}

// Accept reports whether a path should be indexed. Path may be absolute or
// project-root-relative; glob matching runs against both the full path and
// the basename so base-only patterns like *.sum also catch vendored paths.
func (f Filter) Accept(path string) bool {
	for _, g := range f.Excludes {
		if globMatch(g, path) {
			return false
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if f.MaxBytes > 0 && info.Size() > f.MaxBytes {
		return false
	}
	fh, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = fh.Close() }()
	buf := make([]byte, 1024)
	n, _ := io.ReadFull(fh, buf)
	if n > 0 && bytes.Contains(buf[:n], []byte{0}) {
		return false
	}
	return true
}

// globMatch supports ** (any number of path components) in addition to
// stdlib path.Match syntax. Matches against both the full path and the
// basename.
func globMatch(pattern, path string) bool {
	if strings.Contains(pattern, "**") {
		// "prefix/**" matches any path starting with prefix/ or equal to prefix.
		parts := strings.SplitN(pattern, "**", 2)
		prefix := strings.TrimSuffix(parts[0], "/")
		suffix := strings.TrimPrefix(parts[1], "/")
		// Normalize: compare against path either way.
		if prefix != "" && !strings.HasPrefix(path, prefix+"/") && !strings.Contains(path, "/"+prefix+"/") && !hasSegment(path, prefix) {
			return false
		}
		if suffix == "" {
			return true
		}
		ok, _ := filepath.Match(suffix, filepath.Base(path))
		return ok
	}
	// No **: try full path, then basename.
	if ok, _ := filepath.Match(pattern, path); ok {
		return true
	}
	if ok, _ := filepath.Match(pattern, filepath.Base(path)); ok {
		return true
	}
	return false
}

// hasSegment checks whether prefix appears as a directory segment in path.
func hasSegment(path, segment string) bool {
	for _, part := range strings.Split(path, string(filepath.Separator)) {
		if part == segment {
			return true
		}
	}
	return false
}
