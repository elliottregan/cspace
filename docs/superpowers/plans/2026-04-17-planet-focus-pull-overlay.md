# Planet Focus-Pull Overlay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the raw log stream during `cspace up` provisioning with a bubbletea alt-screen overlay showing the instance's planet "coming into focus" across 14 phases, with a clean handoff to Claude on success and a bordered error panel on failure.

**Architecture:** `provision.Run` grows a `Reporter` interface and `Stdout`/`Stderr` writers in `Params`. `cspace up` creates a buffered event channel and a bubbletea `tea.Program` with `tea.WithAltScreen()`, then runs provisioning in a goroutine while the overlay consumes events. Six palette stages (block → half-block → braille) combined with a per-phase RGB lerp from grey to the planet's canonical color create the focus-pull effect. `--verbose`, non-TTY stdout, and terminals smaller than 40×30 bypass the overlay entirely.

**Tech Stack:** Go 1.25 + `github.com/charmbracelet/bubbletea` v1.3.10, `github.com/charmbracelet/bubbles/spinner`, `github.com/charmbracelet/lipgloss` v1.1.0 (already direct dep), `golang.org/x/term` (terminal size). Bubbletea and bubbles are currently transitive via huh; we promote them to direct deps.

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `lib/planets.json` | Create | Canonical planet config. Synced into `internal/assets/embedded/` by `make sync-embedded`, read by Go via `assets.EmbeddedFS`. Future: shell `statusline.sh` reads via `jq`. |
| `Makefile` | Modify | Add `cp lib/planets.json internal/assets/embedded/` to `sync-embedded` target. |
| `internal/planets/planets.go` | Create | `Planet` struct, `Get`, `MustGet`. Loads from `assets.EmbeddedFS`. |
| `internal/planets/shapes.go` | Create | `Shape` type (`[6][12]float64`), `GetShape(name)` returning per-planet shade grid. |
| `internal/planets/planets_test.go` | Create | Unit tests for Get, MustGet, GetShape. |
| `internal/overlay/render.go` | Create | `LerpColor`, `ShadeToChar`, `RenderPlanet` — pure functions, easy to test. |
| `internal/overlay/render_test.go` | Create | Unit tests for all render functions. |
| `internal/overlay/overlay.go` | Create | Bubbletea model, `ProvisionEvent` types, `ChannelReporter`, `NewModel`, `Run`. |
| `internal/overlay/overlay_test.go` | Create | `View()` output tests for loading/error states. |
| `internal/provision/provision.go` | Modify | Add `Reporter` interface, `logReporter`, `Stdout`/`Stderr` fields on `Params`, phase callbacks at each boundary, tracked-phase error defer. |
| `internal/cli/up.go` | Modify | Add `--verbose` flag, terminal size check, wire channel reporter + overlay goroutine for TTY path. |
| `go.mod` / `go.sum` | Modify (via `go get`) | Promote `bubbletea` and `bubbles` to direct deps; add `golang.org/x/term`. |

---

## Conventions

- After any change to `lib/` run `make sync-embedded` (build does this automatically).
- After any Go change run `make vet && make test` before committing.
- Keep commit messages short imperative ("Add planets package", "Wire overlay into cspace up").
- All file paths in this plan are relative to `/workspace`.

---

## Task 1: Planets package + canonical JSON

**Files:**
- Create: `lib/planets.json`
- Create: `internal/planets/planets.go`
- Create: `internal/planets/planets_test.go`
- Modify: `Makefile` (one line)
- Modify: `go.mod` / `go.sum` (via `go get`)

- [ ] **Step 1: Write `lib/planets.json`**

```json
{
  "planets": {
    "mercury": { "symbol": "☿", "color": [169, 169, 169], "accent": [120, 120, 120] },
    "venus":   { "symbol": "♀", "color": [237, 214, 153], "accent": [200, 170, 110] },
    "earth":   { "symbol": "♁", "color": [78, 159, 222],  "accent": [60, 130, 90]   },
    "mars":    { "symbol": "♂", "color": [193, 68, 14],   "accent": [220, 180, 160] },
    "jupiter": { "symbol": "♃", "color": [200, 133, 44],  "accent": [150, 90, 40]   },
    "saturn":  { "symbol": "♄", "color": [212, 180, 131], "accent": [170, 140, 100] },
    "uranus":  { "symbol": "♅", "color": [127, 223, 223], "accent": [90, 170, 170]  },
    "neptune": { "symbol": "♆", "color": [63, 84, 186],   "accent": [40, 60, 140]   }
  }
}
```

- [ ] **Step 2: Extend the `sync-embedded` Makefile target**

Add this line inside the `sync-embedded:` recipe in `Makefile`, immediately after the existing `@cp lib/defaults.json internal/assets/embedded/` line (around line 21):

```makefile
	@cp lib/planets.json internal/assets/embedded/
```

Then run:

```bash
make sync-embedded
ls internal/assets/embedded/planets.json
```

Expected: file exists.

- [ ] **Step 3: Write the failing test `internal/planets/planets_test.go`**

```go
package planets

import "testing"

func TestGetMercury(t *testing.T) {
	p, ok := Get("mercury")
	if !ok {
		t.Fatal("expected mercury to be present")
	}
	if p.Symbol != "☿" {
		t.Errorf("symbol: got %q, want ☿", p.Symbol)
	}
	if p.Color != [3]uint8{169, 169, 169} {
		t.Errorf("color: got %v, want [169 169 169]", p.Color)
	}
}

func TestGetUnknown(t *testing.T) {
	if _, ok := Get("pluto"); ok {
		t.Error("expected pluto to be absent")
	}
}

func TestMustGetFallback(t *testing.T) {
	p := MustGet("pluto")
	if p.Symbol == "" {
		t.Error("expected non-empty fallback symbol")
	}
}

func TestAllPlanetsPresent(t *testing.T) {
	want := []string{"mercury", "venus", "earth", "mars", "jupiter", "saturn", "uranus", "neptune"}
	for _, name := range want {
		if _, ok := Get(name); !ok {
			t.Errorf("missing planet %q", name)
		}
	}
}
```

- [ ] **Step 4: Run the test to see it fail**

```bash
go test ./internal/planets/...
```

Expected: FAIL with "package internal/planets: build failed" (no planets.go yet).

- [ ] **Step 5: Implement `internal/planets/planets.go`**

```go
// Package planets loads the canonical planet visual config shared between
// the provisioning overlay and the in-container statusline.
package planets

import (
	"encoding/json"
	"fmt"

	"github.com/elliottregan/cspace/internal/assets"
)

// Planet describes how a single planet renders: unicode symbol, canonical
// 24-bit color, and a complementary accent color for band/detail shading.
type Planet struct {
	Symbol string   `json:"symbol"`
	Color  [3]uint8 `json:"color"`
	Accent [3]uint8 `json:"accent"`
}

type planetsFile struct {
	Planets map[string]Planet `json:"planets"`
}

var all map[string]Planet

func init() {
	data, err := assets.EmbeddedFS.ReadFile("embedded/planets.json")
	if err != nil {
		panic(fmt.Sprintf("planets: reading embedded planets.json: %v", err))
	}
	var f planetsFile
	if err := json.Unmarshal(data, &f); err != nil {
		panic(fmt.Sprintf("planets: unmarshal planets.json: %v", err))
	}
	all = f.Planets
}

// Get returns the Planet config for name. The second return value is false
// when the name is not a known planet.
func Get(name string) (Planet, bool) {
	p, ok := all[name]
	return p, ok
}

// MustGet returns Get(name) on hit, or a neutral grey fallback otherwise.
// Callers use this when the instance name was user-provided and may not
// correspond to a known planet (e.g. a custom instance name like "ci-bot").
func MustGet(name string) Planet {
	if p, ok := all[name]; ok {
		return p
	}
	return Planet{
		Symbol: "●",
		Color:  [3]uint8{128, 128, 128},
		Accent: [3]uint8{90, 90, 90},
	}
}
```

- [ ] **Step 6: Run the test to see it pass**

```bash
make sync-embedded
go test ./internal/planets/...
```

Expected: PASS on all four tests.

- [ ] **Step 7: Promote bubbletea, bubbles, and x/term to direct deps**

```bash
cd /workspace
go get github.com/charmbracelet/bubbletea@v1.3.10
go get github.com/charmbracelet/bubbles/spinner
go get golang.org/x/term
go mod tidy
```

Then verify:

```bash
grep -E "bubbletea|bubbles|golang.org/x/term" go.mod
```

Expected: all three appear outside `// indirect` blocks.

- [ ] **Step 8: Commit**

```bash
git add lib/planets.json internal/planets/ Makefile go.mod go.sum internal/assets/embedded/planets.json
git commit -m "Add planets package with canonical visual config"
```

Note: `internal/assets/embedded/planets.json` is a build product but is committed because `.gitkeep` lives alongside it and sync-embedded writes into the tree. If your repo .gitignores that path, drop it from the `git add`.

---

## Task 2: Shape maps (all 8 planets)

**Files:**
- Create: `internal/planets/shapes.go`
- Modify: `internal/planets/planets_test.go` (add shape tests)

- [ ] **Step 1: Add failing test for `GetShape`**

Append to `internal/planets/planets_test.go`:

```go
func TestGetShapeDimensions(t *testing.T) {
	names := []string{"mercury", "venus", "earth", "mars", "jupiter", "saturn", "uranus", "neptune"}
	for _, name := range names {
		s := GetShape(name)
		if len(s) != 6 {
			t.Errorf("%s: got %d rows, want 6", name, len(s))
		}
		for r, row := range s {
			if len(row) != 12 {
				t.Errorf("%s row %d: got %d cols, want 12", name, r, len(row))
			}
		}
	}
}

func TestGetShapeUnknown(t *testing.T) {
	// Unknown names should fall back to the mercury shape so custom
	// instance names still render something.
	s := GetShape("ci-bot")
	if len(s) != 6 {
		t.Errorf("fallback shape: got %d rows, want 6", len(s))
	}
}

func TestShapeValuesInRange(t *testing.T) {
	s := GetShape("mercury")
	for r, row := range s {
		for c, v := range row {
			if v < 0 || v > 1 {
				t.Errorf("mercury[%d][%d] = %v, must be in [0,1]", r, c, v)
			}
		}
	}
}
```

- [ ] **Step 2: Run tests to see failure**

```bash
go test ./internal/planets/...
```

Expected: FAIL with "undefined: GetShape".

- [ ] **Step 3: Implement `internal/planets/shapes.go`**

Each shape is a 6-row × 12-col grid of shade values in [0.0, 1.0]. 0.0 = empty space, 1.0 = brightest center. Rows are terminal lines; cols are single terminal chars. Chars are roughly 2:1 height-to-width so a 12×6 grid renders as a roughly circular disk.

```go
package planets

// Shape is a 6-row × 12-col grid of shade values in [0.0, 1.0] describing
// how a planet renders. Each cell is one terminal character. 0.0 = empty,
// 1.0 = maximum intensity.
type Shape = [6][12]float64

// mercurySimpleSphere — featureless silvery sphere, linear falloff.
var mercurySimpleSphere = Shape{
	{0.0, 0.0, 0.0, 0.2, 0.5, 0.6, 0.6, 0.5, 0.2, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.4, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.4, 0.0, 0.0},
	{0.0, 0.3, 0.7, 0.9, 1.0, 1.0, 1.0, 0.9, 0.7, 0.5, 0.2, 0.0},
	{0.0, 0.3, 0.7, 0.9, 1.0, 1.0, 1.0, 0.9, 0.7, 0.5, 0.2, 0.0},
	{0.0, 0.0, 0.4, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.4, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.2, 0.5, 0.6, 0.6, 0.5, 0.2, 0.0, 0.0, 0.0},
}

// venusUniformHaze — thick bright atmosphere, nearly uniform disk.
var venusUniformHaze = Shape{
	{0.0, 0.0, 0.0, 0.4, 0.6, 0.7, 0.7, 0.6, 0.4, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.5, 0.8, 0.9, 0.9, 0.9, 0.9, 0.8, 0.5, 0.0, 0.0},
	{0.0, 0.4, 0.8, 0.9, 0.9, 1.0, 1.0, 0.9, 0.9, 0.7, 0.3, 0.0},
	{0.0, 0.4, 0.8, 0.9, 0.9, 1.0, 1.0, 0.9, 0.9, 0.7, 0.3, 0.0},
	{0.0, 0.0, 0.5, 0.8, 0.9, 0.9, 0.9, 0.9, 0.8, 0.5, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.4, 0.6, 0.7, 0.7, 0.6, 0.4, 0.0, 0.0, 0.0},
}

// earthContinents — irregular shading suggests land/sea contrast.
var earthContinents = Shape{
	{0.0, 0.0, 0.0, 0.3, 0.6, 0.7, 0.7, 0.6, 0.3, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.5, 0.8, 0.7, 0.9, 0.9, 0.8, 0.6, 0.3, 0.0, 0.0},
	{0.0, 0.3, 0.6, 0.7, 0.9, 1.0, 0.8, 0.9, 0.7, 0.4, 0.1, 0.0},
	{0.0, 0.2, 0.7, 0.9, 0.8, 1.0, 1.0, 0.7, 0.8, 0.5, 0.2, 0.0},
	{0.0, 0.0, 0.4, 0.7, 0.9, 0.8, 0.9, 0.8, 0.6, 0.3, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.2, 0.5, 0.6, 0.6, 0.5, 0.2, 0.0, 0.0, 0.0},
}

// marsPolarCap — top row slightly brighter than bottom, hint of northern ice cap.
var marsPolarCap = Shape{
	{0.0, 0.0, 0.0, 0.3, 0.6, 0.8, 0.8, 0.6, 0.3, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.4, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.4, 0.0, 0.0},
	{0.0, 0.3, 0.6, 0.8, 0.9, 1.0, 1.0, 0.9, 0.7, 0.4, 0.1, 0.0},
	{0.0, 0.2, 0.5, 0.8, 0.9, 1.0, 1.0, 0.9, 0.7, 0.4, 0.1, 0.0},
	{0.0, 0.0, 0.3, 0.6, 0.7, 0.8, 0.8, 0.7, 0.5, 0.3, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.2, 0.4, 0.5, 0.5, 0.4, 0.2, 0.0, 0.0, 0.0},
}

// jupiterBands — alternating rows with different core intensity read as horizontal bands.
var jupiterBands = Shape{
	{0.0, 0.0, 0.0, 0.4, 0.6, 0.7, 0.7, 0.6, 0.4, 0.0, 0.0, 0.0},
	{0.0, 0.2, 0.5, 0.8, 1.0, 1.0, 1.0, 0.9, 0.6, 0.2, 0.0, 0.0},
	{0.0, 0.2, 0.6, 0.8, 0.9, 1.0, 1.0, 0.8, 0.7, 0.4, 0.1, 0.0},
	{0.0, 0.1, 0.5, 0.8, 1.0, 1.0, 1.0, 0.9, 0.6, 0.3, 0.0, 0.0},
	{0.0, 0.0, 0.4, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.4, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.4, 0.6, 0.7, 0.7, 0.6, 0.4, 0.0, 0.0, 0.0},
}

// saturnRings — sphere in middle 4 rows, ring extends to cols 0-1 and 10-11 on the equator.
var saturnRings = Shape{
	{0.0, 0.0, 0.0, 0.0, 0.4, 0.6, 0.6, 0.4, 0.0, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.2, 0.6, 0.9, 1.0, 1.0, 0.9, 0.6, 0.2, 0.0, 0.0},
	{0.3, 0.4, 0.5, 0.7, 0.9, 1.0, 1.0, 0.9, 0.7, 0.5, 0.4, 0.3},
	{0.3, 0.4, 0.5, 0.7, 0.9, 1.0, 1.0, 0.9, 0.7, 0.5, 0.4, 0.3},
	{0.0, 0.0, 0.2, 0.6, 0.9, 1.0, 1.0, 0.9, 0.6, 0.2, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.0, 0.4, 0.6, 0.6, 0.4, 0.0, 0.0, 0.0, 0.0},
}

// uranusSmallSphere — smaller, uniform disk (Uranus reads as a featureless blue-green ball).
var uranusSmallSphere = Shape{
	{0.0, 0.0, 0.0, 0.2, 0.5, 0.6, 0.6, 0.5, 0.2, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.3, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.3, 0.0, 0.0},
	{0.0, 0.2, 0.6, 0.8, 0.9, 1.0, 1.0, 0.9, 0.8, 0.6, 0.2, 0.0},
	{0.0, 0.2, 0.6, 0.8, 0.9, 1.0, 1.0, 0.9, 0.8, 0.6, 0.2, 0.0},
	{0.0, 0.0, 0.3, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.3, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.2, 0.5, 0.6, 0.6, 0.5, 0.2, 0.0, 0.0, 0.0},
}

// neptuneDenseCore — smaller, darker-edged disk with bright concentrated core.
var neptuneDenseCore = Shape{
	{0.0, 0.0, 0.0, 0.0, 0.3, 0.5, 0.5, 0.3, 0.0, 0.0, 0.0, 0.0},
	{0.0, 0.0, 0.2, 0.5, 0.7, 0.8, 0.8, 0.7, 0.5, 0.2, 0.0, 0.0},
	{0.0, 0.1, 0.5, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.5, 0.1, 0.0},
	{0.0, 0.1, 0.5, 0.7, 0.8, 0.9, 0.9, 0.8, 0.7, 0.5, 0.1, 0.0},
	{0.0, 0.0, 0.2, 0.5, 0.7, 0.8, 0.8, 0.7, 0.5, 0.2, 0.0, 0.0},
	{0.0, 0.0, 0.0, 0.0, 0.3, 0.5, 0.5, 0.3, 0.0, 0.0, 0.0, 0.0},
}

var shapes = map[string]Shape{
	"mercury": mercurySimpleSphere,
	"venus":   venusUniformHaze,
	"earth":   earthContinents,
	"mars":    marsPolarCap,
	"jupiter": jupiterBands,
	"saturn":  saturnRings,
	"uranus":  uranusSmallSphere,
	"neptune": neptuneDenseCore,
}

// GetShape returns the shade grid for the named planet. Unknown names fall
// back to the mercury sphere so custom instance names still render.
func GetShape(name string) Shape {
	if s, ok := shapes[name]; ok {
		return s
	}
	return mercurySimpleSphere
}
```

- [ ] **Step 4: Run tests to see pass**

```bash
go test ./internal/planets/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/planets/shapes.go internal/planets/planets_test.go
git commit -m "Add planet shape maps for focus-pull animation"
```

---

## Task 3: Render primitives (LerpColor, ShadeToChar, RenderPlanet)

**Files:**
- Create: `internal/overlay/render.go`
- Create: `internal/overlay/render_test.go`

- [ ] **Step 1: Write failing tests `internal/overlay/render_test.go`**

```go
package overlay

import (
	"strings"
	"testing"

	"github.com/elliottregan/cspace/internal/planets"
)

func TestLerpColorEndpoints(t *testing.T) {
	from := [3]uint8{0, 0, 0}
	to := [3]uint8{100, 200, 50}

	if got := LerpColor(from, to, 0); got != from {
		t.Errorf("t=0: got %v, want %v", got, from)
	}
	if got := LerpColor(from, to, 1); got != to {
		t.Errorf("t=1: got %v, want %v", got, to)
	}
	got := LerpColor(from, to, 0.5)
	if got != [3]uint8{50, 100, 25} {
		t.Errorf("t=0.5: got %v, want [50 100 25]", got)
	}
}

func TestLerpColorReverseDirection(t *testing.T) {
	// Lerping from a lighter to a darker channel must not underflow uint8.
	from := [3]uint8{200, 200, 200}
	to := [3]uint8{50, 50, 50}
	got := LerpColor(from, to, 0.5)
	if got[0] != 125 || got[1] != 125 || got[2] != 125 {
		t.Errorf("got %v, want [125 125 125]", got)
	}
}

func TestShadeToCharZero(t *testing.T) {
	for phase := 1; phase <= 6; phase++ {
		if got := ShadeToChar(0, phase); got != " " {
			t.Errorf("phase %d shade 0: got %q, want \" \"", phase, got)
		}
	}
}

func TestShadeToCharNonZero(t *testing.T) {
	for phase := 1; phase <= 6; phase++ {
		if got := ShadeToChar(1.0, phase); got == " " {
			t.Errorf("phase %d shade 1.0: got space, want non-space", phase)
		}
	}
}

func TestShadeToCharPhase1Binary(t *testing.T) {
	// Phase 1 palette has only "█" for non-zero shade.
	for _, v := range []float64{0.1, 0.5, 0.9, 1.0} {
		if got := ShadeToChar(v, 1); got != "█" {
			t.Errorf("phase 1 shade %.1f: got %q, want \"█\"", v, got)
		}
	}
}

func TestRenderPlanetDimensions(t *testing.T) {
	p := planets.MustGet("mercury")
	shape := planets.GetShape("mercury")
	out := RenderPlanet(shape, p, 1, 14)
	lines := strings.Split(out, "\n")
	if len(lines) != 6 {
		t.Errorf("got %d lines, want 6", len(lines))
	}
}

func TestRenderPlanetChangesAcrossPhases(t *testing.T) {
	p := planets.MustGet("mercury")
	shape := planets.GetShape("mercury")
	early := RenderPlanet(shape, p, 1, 14)
	late := RenderPlanet(shape, p, 14, 14)
	if early == late {
		t.Error("expected render to differ between phase 1 and phase 14")
	}
}

func TestRenderPlanetContainsAnsiColor(t *testing.T) {
	p := planets.MustGet("mercury")
	shape := planets.GetShape("mercury")
	out := RenderPlanet(shape, p, 14, 14)
	if !strings.Contains(out, "\x1b[38;2;") {
		t.Error("expected truecolor ANSI escape in output")
	}
	if !strings.Contains(out, "\x1b[0m") {
		t.Error("expected ANSI reset in output")
	}
}
```

- [ ] **Step 2: Run tests to see failure**

```bash
go test ./internal/overlay/...
```

Expected: FAIL with "undefined: LerpColor".

- [ ] **Step 3: Implement `internal/overlay/render.go`**

```go
// Package overlay renders the bubbletea provisioning overlay — a planet
// "coming into focus" through six palette stages while color lerps from
// grey to the planet's canonical RGB.
package overlay

import (
	"fmt"
	"strings"

	"github.com/elliottregan/cspace/internal/planets"
)

// greyStart is the dim grey color the focus-pull begins with before
// interpolating toward the planet's canonical color as phases advance.
var greyStart = [3]uint8{85, 85, 85}

// palettes[phase-1] is the lookup table used during phase N (1..6).
// Index 0 is always the space rendered for shade 0; subsequent indices
// map non-zero shade values (split evenly across [0,1]) to progressively
// denser characters. See the issue for the "block → half-block → braille"
// progression.
var palettes = [6][]string{
	{" ", "█"},
	{" ", "▓", "█"},
	{" ", "▒", "▓", "█"},
	{" ", "░", "▒", "▓", "█"},
	{" ", "░", "▒", "▓", "▌", "█"},
	{" ", "⠂", "░", "▒", "▓", "▌", "█"},
}

// LerpColor linearly interpolates each channel of from→to by t∈[0,1].
// Values outside [0,1] are clamped. Channel arithmetic uses float64 to
// avoid uint8 underflow when a channel shrinks (e.g. 200 → 50).
func LerpColor(from, to [3]uint8, t float64) [3]uint8 {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	lerp := func(a, b uint8) uint8 {
		v := float64(a) + t*(float64(b)-float64(a))
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	return [3]uint8{
		lerp(from[0], to[0]),
		lerp(from[1], to[1]),
		lerp(from[2], to[2]),
	}
}

// ShadeToChar maps a shade in [0,1] to a single character chosen from the
// palette for the given phase (1..6). Phases outside the range are clamped.
// Shade <= 0 always maps to " ".
func ShadeToChar(shade float64, phase int) string {
	if shade <= 0 {
		return " "
	}
	if phase < 1 {
		phase = 1
	}
	if phase > 6 {
		phase = 6
	}
	pal := palettes[phase-1]
	// Skip the leading " " for non-zero shades so a low but positive shade
	// always renders a visible glyph.
	nonBlank := pal[1:]
	idx := int(shade * float64(len(nonBlank)))
	if idx >= len(nonBlank) {
		idx = len(nonBlank) - 1
	}
	return nonBlank[idx]
}

// ansiColor returns the 24-bit foreground SGR escape for rgb.
func ansiColor(rgb [3]uint8) string {
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", rgb[0], rgb[1], rgb[2])
}

// ansiReset restores default SGR.
const ansiReset = "\x1b[0m"

// RenderPlanet returns the colored ASCII art for one frame of the focus-pull
// animation. Color lerps from grey to planet.Color by (phase/total) and the
// palette densifies as phase grows. The return value is a 6-line string with
// each non-empty cell wrapped in truecolor ANSI and a trailing reset.
func RenderPlanet(shape planets.Shape, p planets.Planet, phase, total int) string {
	if total <= 0 {
		total = 1
	}
	t := float64(phase) / float64(total)
	rgb := LerpColor(greyStart, p.Color, t)
	color := ansiColor(rgb)

	var rows []string
	for _, row := range shape {
		var line strings.Builder
		for _, shade := range row {
			ch := ShadeToChar(shade, phaseStage(phase, total))
			if ch == " " {
				line.WriteByte(' ')
				continue
			}
			line.WriteString(color)
			line.WriteString(ch)
			line.WriteString(ansiReset)
		}
		rows = append(rows, line.String())
	}
	return strings.Join(rows, "\n")
}

// phaseStage maps the provisioning phase index (1..total) to a 1..6 palette
// stage. With 14 provisioning phases, each palette stage covers ~2.3 phases.
// Phase 0 is treated as stage 1.
func phaseStage(phase, total int) int {
	if phase < 1 {
		return 1
	}
	if total < 1 {
		total = 1
	}
	stage := 1 + (phase-1)*6/total
	if stage < 1 {
		stage = 1
	}
	if stage > 6 {
		stage = 6
	}
	return stage
}
```

- [ ] **Step 4: Run tests to see pass**

```bash
go test ./internal/overlay/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/overlay/render.go internal/overlay/render_test.go
git commit -m "Add planet render primitives for focus-pull overlay"
```

---

## Task 4: Bubbletea overlay model (skeleton — loading + error states)

**Files:**
- Create: `internal/overlay/overlay.go`
- Create: `internal/overlay/overlay_test.go`

- [ ] **Step 1: Write failing tests `internal/overlay/overlay_test.go`**

```go
package overlay

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/elliottregan/cspace/internal/planets"
)

func newTestModel() Model {
	return NewModel(ModelConfig{
		Name:   "mercury",
		Planet: planets.MustGet("mercury"),
		Total:  14,
		Events: make(chan ProvisionEvent, 4),
		Now:    func() time.Time { return time.Unix(0, 0) },
	})
}

func TestViewShowsInstanceName(t *testing.T) {
	m := newTestModel()
	m.phase = "Validating"
	m.phaseNum = 1
	out := m.View()
	if !strings.Contains(out, "mercury") {
		t.Error("expected instance name in view")
	}
}

func TestViewShowsPhaseName(t *testing.T) {
	m := newTestModel()
	m.phase = "Installing plugins"
	m.phaseNum = 14
	out := m.View()
	if !strings.Contains(out, "Installing plugins") {
		t.Error("expected phase name in view")
	}
}

func TestViewShowsElapsed(t *testing.T) {
	now := time.Unix(0, 0)
	m := NewModel(ModelConfig{
		Name:   "mercury",
		Planet: planets.MustGet("mercury"),
		Total:  14,
		Events: make(chan ProvisionEvent, 4),
		Now:    func() time.Time { return now.Add(107 * time.Second) },
	})
	m.phase = "Validating"
	m.phaseNum = 1
	out := m.View()
	if !strings.Contains(out, "01:47") {
		t.Errorf("expected elapsed 01:47 in view, got:\n%s", out)
	}
}

func TestViewErrorPanel(t *testing.T) {
	m := newTestModel()
	m.err = errors.New("compose up failed: exit 1")
	m.errPhase = "Starting containers"
	out := m.View()
	for _, want := range []string{
		"Provisioning failed",
		"Starting containers",
		"--verbose",
		"Press any key",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("error panel missing %q\nfull view:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run tests to see failure**

```bash
go test ./internal/overlay/...
```

Expected: FAIL with "undefined: NewModel".

- [ ] **Step 3: Implement `internal/overlay/overlay.go`**

```go
package overlay

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/elliottregan/cspace/internal/planets"
)

// EventKind distinguishes provisioning events received by the overlay.
type EventKind int

const (
	// PhaseEvent announces that provisioning has entered a new phase.
	PhaseEvent EventKind = iota
	// WarnEvent carries a provisioning warning (non-fatal).
	WarnEvent
	// DoneEvent signals successful completion.
	DoneEvent
	// ErrorEvent signals a fatal provisioning failure.
	ErrorEvent
)

// ProvisionEvent is the message sent from provision.Run to the overlay over
// a buffered channel. One goroutine writes; bubbletea reads.
type ProvisionEvent struct {
	Kind    EventKind
	Phase   string
	Num     int
	Total   int
	Message string
	Err     error
}

// ChannelReporter implements provision.Reporter by pushing events into a
// buffered channel. It tracks the most-recent phase name so Error() can
// report which phase failed.
type ChannelReporter struct {
	events     chan<- ProvisionEvent
	lastPhase  string
	totalHint  int
}

// NewChannelReporter builds a reporter that writes into events. The total
// hint is the total number of expected phases; callers pass 14 today.
func NewChannelReporter(events chan<- ProvisionEvent, total int) *ChannelReporter {
	return &ChannelReporter{events: events, totalHint: total}
}

// Phase records the current phase name and dispatches a PhaseEvent.
func (r *ChannelReporter) Phase(name string, num, total int) {
	r.lastPhase = name
	r.events <- ProvisionEvent{
		Kind:  PhaseEvent,
		Phase: name,
		Num:   num,
		Total: total,
	}
}

// Warn dispatches a WarnEvent. The overlay currently ignores warnings, but
// they're captured so a future version could render a warning stack.
func (r *ChannelReporter) Warn(msg string) {
	r.events <- ProvisionEvent{Kind: WarnEvent, Message: msg}
}

// Done dispatches a DoneEvent. Caller should not send further events.
func (r *ChannelReporter) Done() {
	r.events <- ProvisionEvent{Kind: DoneEvent}
}

// Error dispatches an ErrorEvent using the most recently reported phase
// when phase is empty (e.g. when called from a deferred error handler).
func (r *ChannelReporter) Error(phase string, err error) {
	if phase == "" {
		phase = r.lastPhase
	}
	r.events <- ProvisionEvent{Kind: ErrorEvent, Phase: phase, Err: err}
}

// ModelConfig bundles the constructor arguments for NewModel so callers
// (and tests) do not need to remember field order.
type ModelConfig struct {
	Name   string
	Planet planets.Planet
	Total  int
	Events <-chan ProvisionEvent
	Now    func() time.Time // injectable for tests
}

// Model is the bubbletea model driving the provisioning overlay.
type Model struct {
	cfg      ModelConfig
	phase    string
	phaseNum int
	start    time.Time
	err      error
	errPhase string
	done     bool
	spinner  spinner.Model
	width    int
	height   int
}

// NewModel constructs a Model with sensible defaults. Events must be a
// channel the caller feeds from provision.Run goroutines.
func NewModel(cfg ModelConfig) Model {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Total <= 0 {
		cfg.Total = 14
	}
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	return Model{
		cfg:     cfg,
		start:   cfg.Now(),
		spinner: sp,
	}
}

// tickMsg fires once per second to keep the elapsed timer fresh.
type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// waitForEvent returns a command that blocks on the events channel and
// converts the next event into a tea.Msg so bubbletea can dispatch it.
func waitForEvent(events <-chan ProvisionEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-events
		if !ok {
			// Channel closed without a Done event — treat as done.
			return ProvisionEvent{Kind: DoneEvent}
		}
		return ev
	}
}

// Init is part of the tea.Model interface.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		tickCmd(),
		waitForEvent(m.cfg.Events),
	)
}

// Update is part of the tea.Model interface.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.err != nil {
			// Any key dismisses the error panel.
			m.done = true
			return m, tea.Quit
		}
		if msg.Type == tea.KeyCtrlC {
			m.done = true
			return m, tea.Quit
		}
		return m, nil

	case tickMsg:
		if m.done {
			return m, nil
		}
		return m, tickCmd()

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case ProvisionEvent:
		switch msg.Kind {
		case PhaseEvent:
			m.phase = msg.Phase
			m.phaseNum = msg.Num
			if msg.Total > 0 {
				m.cfg.Total = msg.Total
			}
			return m, waitForEvent(m.cfg.Events)
		case WarnEvent:
			// Drop silently for now.
			return m, waitForEvent(m.cfg.Events)
		case DoneEvent:
			m.done = true
			return m, tea.Quit
		case ErrorEvent:
			m.err = msg.Err
			m.errPhase = msg.Phase
			// Stay on screen until keypress.
			return m, nil
		}
	}
	return m, nil
}

// View is part of the tea.Model interface.
func (m Model) View() string {
	if m.done && m.err == nil {
		return ""
	}
	if m.err != nil {
		return m.errorView()
	}
	return m.loadingView()
}

var nameStyle = lipgloss.NewStyle().Bold(true)

func (m Model) loadingView() string {
	shape := planets.GetShape(m.cfg.Name)
	art := RenderPlanet(shape, m.cfg.Planet, m.phaseNum, m.cfg.Total)

	elapsed := m.cfg.Now().Sub(m.start)
	mm := int(elapsed.Minutes())
	ss := int(elapsed.Seconds()) % 60
	timer := fmt.Sprintf("%02d:%02d", mm, ss)

	planetColor := lipgloss.Color(fmt.Sprintf("#%02x%02x%02x",
		m.cfg.Planet.Color[0], m.cfg.Planet.Color[1], m.cfg.Planet.Color[2]))
	nameLine := nameStyle.Foreground(planetColor).Render(m.cfg.Name)

	phaseLine := fmt.Sprintf("%s  %s", m.spinner.View(), m.phase)

	content := strings.Join([]string{
		art,
		"",
		nameLine,
		"",
		phaseLine,
		"",
		timer,
	}, "\n")

	if m.width == 0 || m.height == 0 {
		// No WindowSizeMsg yet; return un-centered.
		return content
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, content)
}

var errorPanelStyle = lipgloss.NewStyle().
	Border(lipgloss.RoundedBorder()).
	BorderForeground(lipgloss.Color("9")).
	Padding(1, 2)

func (m Model) errorView() string {
	lines := []string{
		"✗  Provisioning failed",
		"",
		fmt.Sprintf("Phase: %s", m.errPhase),
		"",
		"For the full log, run:",
		fmt.Sprintf("  cspace up %s --verbose", m.cfg.Name),
		"",
		"Press any key to exit",
	}
	panel := errorPanelStyle.Render(strings.Join(lines, "\n"))
	if m.width == 0 || m.height == 0 {
		return panel
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, panel)
}

// Run starts the bubbletea program in alt-screen mode and returns when the
// user dismisses the error panel or the provisioning completes. The ctx is
// currently advisory — bubbletea exits on its own signals — but is reserved
// for future graceful-cancel support.
func Run(ctx context.Context, cfg ModelConfig) error {
	p := tea.NewProgram(NewModel(cfg), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
```

- [ ] **Step 4: Run tests to see pass**

```bash
go test ./internal/overlay/...
```

Expected: PASS on all four overlay tests plus earlier render tests.

- [ ] **Step 5: `go vet` the new package**

```bash
make vet
```

Expected: no output (clean).

- [ ] **Step 6: Commit**

```bash
git add internal/overlay/overlay.go internal/overlay/overlay_test.go
git commit -m "Add bubbletea overlay model for provisioning focus-pull"
```

---

## Task 5: Reporter interface in provision

**Files:**
- Modify: `internal/provision/provision.go`
- Create: `internal/provision/reporter.go`
- Create: `internal/provision/reporter_test.go`

The 14 phases we report, in order (new-instance path) plus idempotent tail. Total = 14.

| # | Label | Source |
|---|---|---|
| 1 | Validating name | `validateName` |
| 2 | Removing orphans | orphan loop |
| 3 | Bundling repo | `gitBundleCreate` |
| 4 | Creating volumes | `ensureVolumes` |
| 5 | Creating network | `NetworkCreate` |
| 6 | Starting reverse proxy | `EnsureProxy` + `NetworkConnect` |
| 7 | Setting up directories | teleport/memory/sessions/context dirs |
| 8 | Starting containers | `compose.Run up -d` |
| 9 | Waiting for container | `WaitForReady` |
| 10 | Configuring hosts | `InjectHosts` |
| 11 | Setting permissions | `chown` |
| 12 | Initializing workspace | `initWorkspace` |
| 13 | Configuring git & env | `configureGit` + env + GH |
| 14 | Installing plugins | marketplace + plugins + post-setup |

When the container is already running we jump straight to phase 14 (the idempotent tail).

- [ ] **Step 1: Write failing test `internal/provision/reporter_test.go`**

```go
package provision

import "testing"

type captured struct {
	kind  string // "phase", "warn", "done", "error"
	name  string
	num   int
	total int
	err   error
}

type recordingReporter struct{ events []captured }

func (r *recordingReporter) Phase(name string, num, total int) {
	r.events = append(r.events, captured{kind: "phase", name: name, num: num, total: total})
}
func (r *recordingReporter) Warn(msg string) {
	r.events = append(r.events, captured{kind: "warn", name: msg})
}
func (r *recordingReporter) Done() {
	r.events = append(r.events, captured{kind: "done"})
}
func (r *recordingReporter) Error(phase string, err error) {
	r.events = append(r.events, captured{kind: "error", name: phase, err: err})
}

func TestReporterInterfaceImplementations(t *testing.T) {
	// Compile-time assertion: both reporter types implement Reporter.
	var _ Reporter = (*recordingReporter)(nil)
	var _ Reporter = logReporter{}
}

func TestPhasesReference(t *testing.T) {
	// Sanity check: the Phases slice exposes 14 labeled steps.
	if len(Phases) != 14 {
		t.Errorf("Phases: got %d entries, want 14", len(Phases))
	}
	if Phases[0] != "Validating name" {
		t.Errorf("Phases[0]: got %q", Phases[0])
	}
	if Phases[13] != "Installing plugins" {
		t.Errorf("Phases[13]: got %q", Phases[13])
	}
}
```

- [ ] **Step 2: Run the test to see it fail**

```bash
go test ./internal/provision/... -run "TestReporterInterfaceImplementations|TestPhasesReference"
```

Expected: FAIL with "undefined: Reporter" or "undefined: Phases".

- [ ] **Step 3: Create `internal/provision/reporter.go`**

```go
package provision

import (
	"fmt"
	"os"
)

// Reporter receives provisioning progress notifications. Implementations
// choose how to surface them — fmt.Printf (logReporter) or a bubbletea
// channel (overlay.ChannelReporter).
//
// All methods are called from provision.Run's own goroutine; implementations
// that forward to channels should use buffered channels so they never block
// provisioning.
type Reporter interface {
	// Phase announces entry into a named phase (1-indexed).
	Phase(name string, num, total int)
	// Warn surfaces a non-fatal issue.
	Warn(msg string)
	// Done is called once, on successful completion.
	Done()
	// Error is called once, on fatal failure. phase is the last phase
	// that started before the failure ("" = unknown).
	Error(phase string, err error)
}

// Phases lists the human-readable label for each of the 14 provisioning
// phases, in order. Exposed so callers (e.g. the overlay) can show the
// total count ahead of provisioning starting.
var Phases = []string{
	"Validating name",
	"Removing orphans",
	"Bundling repo",
	"Creating volumes",
	"Creating network",
	"Starting reverse proxy",
	"Setting up directories",
	"Starting containers",
	"Waiting for container",
	"Configuring hosts",
	"Setting permissions",
	"Initializing workspace",
	"Configuring git & env",
	"Installing plugins",
}

// logReporter is the default reporter used when Params.Reporter is nil.
// It mimics pre-overlay behavior: plain fmt.Printf lines on stdout,
// warnings on stderr.
type logReporter struct{}

func (logReporter) Phase(name string, num, total int) {
	fmt.Printf("[%d/%d] %s...\n", num, total, name)
}

func (logReporter) Warn(msg string) {
	fmt.Fprintf(os.Stderr, "warning: %s\n", msg)
}

func (logReporter) Done() {
	fmt.Println("Setup complete.")
}

func (logReporter) Error(phase string, err error) {
	if phase != "" {
		fmt.Fprintf(os.Stderr, "error in phase %q: %v\n", phase, err)
	}
}
```

- [ ] **Step 4: Modify `internal/provision/provision.go` — wire reporter through Run**

Add an `io` import and new fields to `Params`:

```go
import (
	// ...existing imports...
	"io"
)

type Params struct {
	Name     string
	Branch   string
	Cfg      *config.Config
	Reporter Reporter  // nil → logReporter{}
	Stdout   io.Writer // subprocess stdout; nil → os.Stdout
	Stderr   io.Writer // subprocess stderr; nil → os.Stderr
}

func (p Params) reporter() Reporter {
	if p.Reporter != nil {
		return p.Reporter
	}
	return logReporter{}
}

func (p Params) stdout() io.Writer {
	if p.Stdout != nil {
		return p.Stdout
	}
	return os.Stdout
}

func (p Params) stderr() io.Writer {
	if p.Stderr != nil {
		return p.Stderr
	}
	return os.Stderr
}
```

Change the `Run` signature to named returns so a deferred reporter finalizer can see both:

```go
func Run(p Params) (result Result, err error) {
	reporter := p.reporter()
	currentPhase := ""
	reportPhase := func(num int, label string) {
		currentPhase = label
		reporter.Phase(label, num, len(Phases))
	}
	reportWarn := func(msg string) { reporter.Warn(msg) }

	defer func() {
		if err != nil {
			reporter.Error(currentPhase, err)
			return
		}
		reporter.Done()
	}()

	name := p.Name
	cfg := p.Cfg

	// Phase 1
	reportPhase(1, Phases[0])
	if err = validateName(name); err != nil {
		return Result{}, err
	}
	// ...rest of the function (see Step 5)
}
```

Then replace each `fmt.Printf("Creating new instance...")` style progress statement with the corresponding `reportPhase(N, Phases[N-1])` call, and each `fmt.Fprintf(os.Stderr, "warning: ...")` with `reportWarn(...)`.

- [ ] **Step 5: Concrete edits to `Run`**

Replace the existing `Run` body with the fully-reported version below. (Diff listed inline; use Edit for each hunk.)

At line ~67 (just after `composeName := cfg.ComposeName(name)`):

```go
	// 2. Check if already running
	if instance.IsRunning(composeName) {
		// Already-running path skips phases 2-13; jump to 14.
		reportPhase(1, "Reusing running container")
	} else {
		created = true
		reportPhase(1, Phases[0]) // redundant with the Phase 1 call above — delete that earlier call
```

Since Phase 1 was already reported before this block, replace the above with: delete the `reportPhase(1, Phases[0])` we added at function top and move it here so the "already running" branch can report a distinct phase-1 label.

Final structure:

```go
func Run(p Params) (result Result, err error) {
	reporter := p.reporter()
	currentPhase := ""
	reportPhase := func(num int, label string) {
		currentPhase = label
		reporter.Phase(label, num, len(Phases))
	}
	reportWarn := func(msg string) { reporter.Warn(msg) }
	defer func() {
		if err != nil {
			reporter.Error(currentPhase, err)
			return
		}
		reporter.Done()
	}()

	name := p.Name
	cfg := p.Cfg

	// Phase 1: validate (runs for both new and reused containers)
	reportPhase(1, Phases[0])
	if err = validateName(name); err != nil {
		return Result{}, err
	}

	composeName := cfg.ComposeName(name)
	created := false

	if !instance.IsRunning(composeName) {
		created = true

		// Phase 2: remove orphans
		reportPhase(2, Phases[1])
		for _, suffix := range []string{"", ".browser"} {
			if err = docker.RemoveOrphanContainer(composeName + suffix); err != nil {
				return Result{}, fmt.Errorf("refusing to provision '%s': %w", name, err)
			}
		}

		// Phase 3: bundle repo
		branch := p.Branch
		if branch == "" {
			branch = gitCurrentBranch(cfg.ProjectRoot)
		}
		remoteURL := gitRemoteURL(cfg.ProjectRoot)
		bundlePath := filepath.Join(os.TempDir(), fmt.Sprintf("cspace-%s.bundle", name))
		reportPhase(3, Phases[2])
		if err = gitBundleCreate(cfg.ProjectRoot, bundlePath, p.stdout(), p.stderr()); err != nil {
			return Result{}, fmt.Errorf("creating git bundle: %w", err)
		}
		defer func() { _ = os.Remove(bundlePath) }()

		// Phase 4: volumes
		reportPhase(4, Phases[3])
		if err = ensureVolumesReported(cfg, reportWarn); err != nil {
			return Result{}, fmt.Errorf("creating volumes: %w", err)
		}

		// Phase 5: network
		reportPhase(5, Phases[4])
		if err = docker.NetworkCreate(cfg.ProjectNetwork(), cfg.InstanceLabel()); err != nil {
			return Result{}, fmt.Errorf("creating project network: %w", err)
		}

		// Phase 6: reverse proxy
		reportPhase(6, Phases[5])
		if perr := docker.EnsureProxy(cfg.AssetsDir); perr != nil {
			reportWarn(fmt.Sprintf("proxy: %v", perr))
		}
		if perr := docker.NetworkConnect(cfg.ProjectNetwork(), docker.ProxyContainerName); perr != nil {
			reportWarn(fmt.Sprintf("connecting proxy to project network: %v", perr))
		}

		// Phase 7: directories
		reportPhase(7, Phases[6])
		tpDir := teleportHostDir()
		if err = ensureTeleportDir(tpDir); err != nil {
			return Result{}, err
		}
		if err = os.Setenv("CSPACE_TELEPORT_DIR", tpDir); err != nil {
			return Result{}, fmt.Errorf("exporting CSPACE_TELEPORT_DIR: %w", err)
		}
		if err = ensureMemoryDir(cfg.ProjectRoot); err != nil {
			return Result{}, err
		}
		if err = ensureSessionsDir(cfg.SessionsDir()); err != nil {
			return Result{}, err
		}
		if err = ensureContextDir(cfg.ProjectRoot); err != nil {
			return Result{}, err
		}

		// Phase 8: compose up
		reportPhase(8, Phases[7])
		if err = compose.Run(name, cfg, "up", "-d"); err != nil {
			return Result{}, fmt.Errorf("starting container: %w", err)
		}

		// Phase 9: readiness
		reportPhase(9, Phases[8])
		if err = WaitForReady(composeName, 120*time.Second); err != nil {
			return Result{}, err
		}

		// Phase 10: hosts
		reportPhase(10, Phases[9])
		if herr := docker.InjectHosts(composeName, cfg.ProjectNetwork()); herr != nil {
			reportWarn(fmt.Sprintf("hosts injection: %v", herr))
		}

		// Phase 11: permissions
		reportPhase(11, Phases[10])
		if _, err = instance.DcExecRoot(composeName, "chown", "-R", "dev:dev", "/workspace", "/home/dev/.claude", "/teleport"); err != nil {
			return Result{}, fmt.Errorf("fixing ownership: %w", err)
		}

		// Phase 12: workspace
		reportPhase(12, Phases[11])
		if err = initWorkspace(composeName, bundlePath, branch, remoteURL); err != nil {
			return Result{}, fmt.Errorf("initializing workspace: %w", err)
		}

		// Phase 13: git & env & GH
		reportPhase(13, Phases[12])
		configureGit(composeName, cfg.ProjectRoot)
		copyEnvFile(composeName, cfg.ProjectRoot, ".env")
		copyEnvFile(composeName, cfg.ProjectRoot, ".env.local")
		if gerr := setupGHAuth(composeName, cfg.ProjectRoot); gerr != nil {
			reportWarn(gerr.Error())
		}
	}

	// Phase 14: idempotent tail (marketplace + plugins + post-setup)
	reportPhase(14, Phases[13])
	if merr := ensureMarketplace(composeName); merr != nil {
		reportWarn(fmt.Sprintf("marketplace setup: %v", merr))
	}
	if ierr := installPlugins(composeName, cfg); ierr != nil {
		reportWarn(fmt.Sprintf("plugin installation: %v", ierr))
	}
	if perr := runPostSetup(composeName, cfg); perr != nil {
		reportWarn(fmt.Sprintf("post-setup: %v", perr))
	}

	return Result{Created: created, Name: name}, nil
}
```

- [ ] **Step 6: Update `gitBundleCreate` to accept explicit writers**

Replace the existing function (line ~361):

```go
func gitBundleCreate(projectRoot, bundlePath string, stdout, stderr io.Writer) error {
	cmd := exec.Command("git", "-C", projectRoot, "bundle", "create", bundlePath, "--all")
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}
```

- [ ] **Step 7: Add `ensureVolumesReported` helper**

The current `ensureVolumes` writes warnings directly to `os.Stderr`. Add a parallel helper that forwards through the reporter:

```go
func ensureVolumesReported(cfg *config.Config, warn func(string)) error {
	for _, vol := range []string{cfg.LogsVolume()} {
		if err := docker.VolumeCreate(vol); err != nil {
			warn(err.Error())
		}
	}
	return nil
}
```

Leave the original `ensureVolumes` untouched for backward compatibility (or delete it if no other caller — grep confirms: `grep -n 'ensureVolumes' internal/provision/*.go`).

- [ ] **Step 8: Run targeted tests**

```bash
make sync-embedded
go test ./internal/provision/... -run "TestReporterInterfaceImplementations|TestPhasesReference"
go vet ./...
```

Expected: PASS on both tests; clean vet.

- [ ] **Step 9: Run the full build and all tests**

```bash
make build
make test
```

Expected: binary built, all tests pass. (Existing provision tests should still pass since behavior is equivalent when Reporter is nil.)

- [ ] **Step 10: Commit**

```bash
git add internal/provision/
git commit -m "Add Reporter interface to provision.Run for overlay support"
```

---

## Task 6: Wire overlay into cspace up

**Files:**
- Modify: `internal/cli/up.go`

- [ ] **Step 1: Add `--verbose` flag and the shared helper**

Edit `newUpCmd()` in `internal/cli/up.go` to add the verbose flag after the existing flag declarations (around line 39):

```go
	cmd.Flags().Bool("verbose", false,
		"Stream raw provisioning output instead of showing the planet overlay. "+
			"Use when debugging provisioning failures or when piping output.")
```

And parse it in `runUp` (around line 50):

```go
	verbose, _ := cmd.Flags().GetBool("verbose")
```

- [ ] **Step 2: Pass `verbose` through to `runUpWithArgs`**

Change the signature from:

```go
func runUpWithArgs(name, branch string, noClaude bool, prompt, promptFile, teleportFrom string, persistent bool) error {
```

to:

```go
func runUpWithArgs(name, branch string, noClaude, verbose bool, prompt, promptFile, teleportFrom string, persistent bool) error {
```

Update the call site inside `runUp` (line 104):

```go
	return runUpWithArgs(name, branch, noClaude, verbose, prompt, promptFile, teleportFrom, persistent)
```

If the TUI calls `runUpWithArgs` elsewhere (check with `grep -rn 'runUpWithArgs' internal/cli/`), pass `false` for verbose from those sites — the TUI is already interactive so overlay is the default.

- [ ] **Step 3: Replace the provision call with overlay-aware wiring**

Inside `runUpWithArgs`, replace the current `provision.Run(...)` call (lines 118-126) with:

```go
	if err := provisionWithUI(name, branch, verbose); err != nil {
		return err
	}
```

Add the helper at the bottom of `up.go`:

```go
// provisionWithUI dispatches to the overlay or the raw log stream based on
// TTY, --verbose, and terminal size. The overlay path runs provision in a
// goroutine while a bubbletea Program consumes a buffered event channel;
// returns the provisioning error (if any) after the overlay exits.
func provisionWithUI(name, branch string, verbose bool) error {
	if shouldUseOverlay(verbose) {
		return provisionWithOverlay(name, branch)
	}
	_, err := provision.Run(provision.Params{
		Name:   name,
		Branch: branch,
		Cfg:    cfg,
	})
	return err
}

func shouldUseOverlay(verbose bool) bool {
	if verbose {
		return false
	}
	if !isInteractive() {
		return false
	}
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 40 || h < 30 {
		return false
	}
	return true
}

func provisionWithOverlay(name, branch string) error {
	events := make(chan overlay.ProvisionEvent, 16)
	reporter := overlay.NewChannelReporter(events, len(provision.Phases))

	var provErr error
	go func() {
		_, provErr = provision.Run(provision.Params{
			Name:     name,
			Branch:   branch,
			Cfg:      cfg,
			Reporter: reporter,
			Stdout:   io.Discard,
			Stderr:   io.Discard,
		})
		close(events)
	}()

	planet := planets.MustGet(name)
	model := overlay.ModelConfig{
		Name:   name,
		Planet: planet,
		Total:  len(provision.Phases),
		Events: events,
	}
	if err := overlay.Run(context.Background(), model); err != nil {
		return err
	}
	return provErr
}
```

- [ ] **Step 4: Add the new imports**

Add to the import block at the top of `internal/cli/up.go`:

```go
	"context"
	"io"

	"golang.org/x/term"

	"github.com/elliottregan/cspace/internal/overlay"
	"github.com/elliottregan/cspace/internal/planets"
```

- [ ] **Step 5: Build**

```bash
make build
```

Expected: binary compiles.

- [ ] **Step 6: Run vet and tests**

```bash
make vet
make test
```

Expected: clean vet; all tests pass.

- [ ] **Step 7: Smoke test the verbose path (no overlay)**

```bash
./bin/cspace-go up --verbose some-test-name --no-claude
```

Expected: plain-text progress lines like `[1/14] Validating name...` through `[14/14] Installing plugins...` then `Setup complete.`. Teardown: `cspace down some-test-name`.

- [ ] **Step 8: Smoke test the overlay path**

Ensure the terminal is at least 80×40, then:

```bash
./bin/cspace-go up --no-claude
```

Expected: alt-screen takes over, planet art (mercury) appears dim grey and blurry, sharpens as phases advance, instance name shown in mercury color, spinner + phase text + elapsed timer centered. At completion: alt-screen exits, control returns to the terminal, "Instance 'mercury' is ready..." line prints normally. Teardown: `cspace down mercury`.

- [ ] **Step 9: Smoke test the error path**

Simulate a failure by pointing cspace at a broken compose file or interrupting docker mid-run. Alternatively, test with a name that collides:

```bash
./bin/cspace-go up --no-claude mercury
# (leave running)
./bin/cspace-go up --no-claude mercury  # second invocation should fail at orphan-removal
```

Expected: the second invocation shows the error panel with "Provisioning failed", the failing phase name, `cspace up mercury --verbose` hint, and "Press any key to exit". Press any key → alt-screen exits, non-zero exit code.

- [ ] **Step 10: Commit**

```bash
git add internal/cli/up.go go.mod go.sum
git commit -m "Wire planet focus-pull overlay into cspace up"
```

---

## Task 7: Final verification pass

**Files:** none modified.

- [ ] **Step 1: `make check`**

```bash
make check
```

Expected: all targets (fmt-check, vet, lint, test) pass. If `make check` is not defined, run each individually:

```bash
make fmt-check
make vet
make test
make lint
```

- [ ] **Step 2: Manually verify rendering across every planet**

For each planet, run `cspace up --no-claude NAME` and visually confirm:
- mercury → silvery-grey disk
- venus → warm pale-yellow disk
- earth → blue-green with irregular shading
- mars → rusty red with hint of polar brightness
- jupiter → orange with visible horizontal banding
- saturn → golden tan with ring protrusion on cols 0-1 and 10-11
- uranus → pale cyan, smaller disk
- neptune → deep blue, dense core

Tear each down with `cspace down NAME` between runs.

- [ ] **Step 3: Verify non-TTY fallback**

```bash
./bin/cspace-go up --no-claude some-test | cat
```

Expected: plain-text log lines (overlay suppressed because stdout isn't a TTY). Teardown: `cspace down some-test`.

- [ ] **Step 4: Verify small-terminal fallback**

Shrink the terminal to ~30 cols × 20 rows, then run `./bin/cspace-go up --no-claude`. Expected: plain-text log lines (overlay suppressed because terminal is too small).

- [ ] **Step 5: Done**

No further commits. The feature is ready. Open a PR with:

```bash
git log --oneline main..HEAD
gh pr create --title "Planet focus-pull overlay during provisioning" --body-file - <<'EOF'
## Summary
- Replaces raw Docker/compose output with a bubbletea alt-screen overlay during `cspace up`.
- Animates the instance's planet "coming into focus" across 14 phases.
- Falls back to verbose output with `--verbose`, non-TTY stdout, or terminals smaller than 40×30.

## Test plan
- [ ] `make check` passes
- [ ] Overlay renders cleanly for all eight planet names
- [ ] `--verbose` streams the original log output
- [ ] Piped stdout (`cspace up | cat`) uses the log path
- [ ] Error panel renders and exits non-zero on provisioning failure

Closes #41
EOF
```

---

## Self-review notes

Re-reading the issue against this plan:

- ✅ Alt-screen (`tea.WithAltScreen()`): Task 4.
- ✅ No boot-log persistence: `io.Discard` for `Stdout`/`Stderr` on overlay path; `--verbose` restores.
- ✅ No progress bar: the view shows spinner + phase text + elapsed, no ratio bar.
- ✅ Non-TTY auto-fallback: `shouldUseOverlay` checks `isInteractive()`.
- ✅ Ctrl+C exits alt-screen: bubbletea handles `tea.KeyCtrlC` → `tea.Quit` in Update.
- ✅ `lib/planets.json` as canonical: Task 1.
- ✅ Focus-pull axes: color lerp (Task 3 `LerpColor`) + palette densification (`phaseStage` + `palettes`).
- ✅ Layout: planet art, instance name, spinner+phase, elapsed, centered with `lipgloss.Place`.
- ✅ Error panel: bordered red box with `cspace up <name> --verbose` hint (Task 4 `errorView`).
- ✅ Build order matches issue: config (T1) → overlay scaffold (T4) → mercury (T2+T3) → remaining planets (T2 covers all eight in one shot because the shape arrays are small and trivial to author together).

- ⚠️ Statusline.sh refactor deferred (issue noted this is optional: "or keep duplicated for now"). The shell still hardcodes colors; future PR can switch to `jq` over `lib/planets.json` once the file is shipped.

- ⚠️ Tier 5 image-based rendering explicitly out of scope per the issue.

- ⚠️ Half-blocks / braille visual consistency is a known open question; the plan commits to the simple ordering `█ → ▓ → ▒ → ░ → ▌ → ⠂` and can be tuned visually after the first end-to-end run.

No placeholder steps remain. Type and method names are consistent across tasks:
- `Reporter` interface (provision) / `ChannelReporter` type (overlay) / `logReporter` type (provision).
- `ProvisionEvent` struct with `EventKind` enum (overlay package).
- `Shape`, `Get`, `MustGet`, `GetShape` in planets package.
- `LerpColor`, `ShadeToChar`, `RenderPlanet` exported from overlay package.
