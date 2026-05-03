// Package planets exposes the canonical planet symbol + color table cspace
// uses to render sandbox names. The data lives in lib/planets.json (embedded
// at build time); this package is the read-side accessor.
//
// Pass 2 (loading overlay) will add the procedural-rendering shapes.go back
// alongside this file. For now we expose only what the CLI's text output
// needs: a unicode glyph and the planet's canonical 24-bit color.
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

// SymbolFor returns the planet glyph for name, or an empty string if name
// is not a known planet — callers that want to prefix text output with a
// symbol can simply concatenate without a nil check.
func SymbolFor(name string) string {
	if p, ok := all[name]; ok {
		return p.Symbol
	}
	return ""
}

// ColorFor returns the planet's canonical 24-bit color and true on hit, or
// the zero color and false when name is not a known planet.
func ColorFor(name string) ([3]uint8, bool) {
	if p, ok := all[name]; ok {
		return p.Color, true
	}
	return [3]uint8{}, false
}
