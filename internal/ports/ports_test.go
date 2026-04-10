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
	tests := []struct {
		name    string
		wantDev int
		wantPre int
	}{
		{"mercury", 5173, 4173},
		{"venus", 5174, 4174},
		{"earth", 5175, 4175},
		{"mars", 5176, 4176},
		{"jupiter", 5177, 4177},
		{"saturn", 5178, 4178},
		{"uranus", 5179, 4179},
		{"neptune", 5180, 4180},
		{"custom", 0, 0},
		{"my-branch", 0, 0},
		{"", 0, 0},
	}
	for _, tt := range tests {
		pm := AssignPorts(tt.name)
		if pm.Dev != tt.wantDev {
			t.Errorf("AssignPorts(%q).Dev = %d, want %d", tt.name, pm.Dev, tt.wantDev)
		}
		if pm.Preview != tt.wantPre {
			t.Errorf("AssignPorts(%q).Preview = %d, want %d", tt.name, pm.Preview, tt.wantPre)
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
