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
