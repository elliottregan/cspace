package ports

import "testing"

func TestIsPlanet(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"mercury", true},
		{"venus", true},
		{"earth", true},
		{"mars", true},
		{"jupiter", true},
		{"saturn", true},
		{"uranus", true},
		{"neptune", true},
		{"pluto", false},
		{"custom-name", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsPlanet(tt.name); got != tt.want {
			t.Errorf("IsPlanet(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestPlanetIndex(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"mercury", 0},
		{"venus", 1},
		{"neptune", 7},
		{"pluto", -1},
		{"", -1},
	}
	for _, tt := range tests {
		if got := PlanetIndex(tt.name); got != tt.want {
			t.Errorf("PlanetIndex(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestAssignPorts(t *testing.T) {
	// All instances get Docker-assigned ports (0) to allow the same planet
	// name to run across multiple projects without port collisions.
	names := []string{"mercury", "venus", "neptune", "custom", "my-branch", ""}
	for _, name := range names {
		pm := AssignPorts(name)
		if pm.Dev != 0 {
			t.Errorf("AssignPorts(%q).Dev = %d, want 0", name, pm.Dev)
		}
		if pm.Preview != 0 {
			t.Errorf("AssignPorts(%q).Preview = %d, want 0", name, pm.Preview)
		}
	}
}

func TestPlanetsCount(t *testing.T) {
	if len(Planets) != 8 {
		t.Errorf("expected 8 planets, got %d", len(Planets))
	}
}

func TestPlanetsOrder(t *testing.T) {
	expected := []string{"mercury", "venus", "earth", "mars", "jupiter", "saturn", "uranus", "neptune"}
	for i, want := range expected {
		if Planets[i] != want {
			t.Errorf("Planets[%d] = %q, want %q", i, Planets[i], want)
		}
	}
}
