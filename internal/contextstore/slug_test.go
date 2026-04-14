package contextstore

import "testing"

func TestSlugify(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"Use Go MCP SDK", "use-go-mcp-sdk"},
		{"  Trim & Collapse!!  ", "trim-collapse"},
		{"Multiple   spaces---and_underscores", "multiple-spaces-and-underscores"},
		{"UPPER/lower:Mixed", "upper-lower-mixed"},
		{"café déjà vu", "caf-d-j-vu"},
		{"", ""},
		{"!!!", ""},
	}
	for _, c := range cases {
		if got := Slugify(c.in); got != c.want {
			t.Errorf("Slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugifyTruncates(t *testing.T) {
	long := "a-very-long-title-that-exceeds-the-sixty-character-limit-imposed-by-the-slug-function"
	got := Slugify(long)
	if len(got) > 60 {
		t.Errorf("Slugify length = %d, want <= 60", len(got))
	}
	if got[len(got)-1] == '-' {
		t.Errorf("Slugify trailing hyphen: %q", got)
	}
}

func TestResolveCollision(t *testing.T) {
	taken := map[string]bool{
		"2026-04-13-foo.md":   true,
		"2026-04-13-foo-2.md": true,
	}
	got := ResolveCollision("2026-04-13-foo", taken)
	if got != "2026-04-13-foo-3" {
		t.Errorf("ResolveCollision = %q, want 2026-04-13-foo-3", got)
	}

	got = ResolveCollision("2026-04-13-bar", taken)
	if got != "2026-04-13-bar" {
		t.Errorf("ResolveCollision = %q, want 2026-04-13-bar", got)
	}
}
