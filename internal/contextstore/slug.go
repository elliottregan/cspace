package contextstore

import (
	"fmt"
	"strings"
)

const maxSlugLen = 60

// Slugify converts a title into a filesystem-safe slug:
// lowercase, non-alphanumeric runs collapsed to single hyphens,
// truncated to maxSlugLen with trailing hyphens trimmed.
func Slugify(title string) string {
	var b strings.Builder
	b.Grow(len(title))
	prevHyphen := true // suppresses leading hyphen
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
			continue
		}
		if !prevHyphen {
			b.WriteByte('-')
			prevHyphen = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > maxSlugLen {
		s = strings.TrimRight(s[:maxSlugLen], "-")
	}
	return s
}

// ResolveCollision returns base if "<base>.md" is not in taken,
// otherwise appends -2, -3, ... until an unused name is found.
func ResolveCollision(base string, taken map[string]bool) string {
	if !taken[base+".md"] {
		return base
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if !taken[candidate+".md"] {
			return candidate
		}
	}
}
