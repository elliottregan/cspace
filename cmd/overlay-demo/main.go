// Command overlay-demo renders the cspace provisioning overlay with
// synthesized phase events so developers can iterate on the UI without
// spinning up a real container. Requires a TTY.
//
// Examples:
//
//	go run ./cmd/overlay-demo/
//	go run ./cmd/overlay-demo/ --planet=jupiter --per-phase=400ms
//	go run ./cmd/overlay-demo/ --fail-at=8
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/elliottregan/cspace/internal/overlay"
	"github.com/elliottregan/cspace/internal/planets"
	"github.com/elliottregan/cspace/internal/provision"
)

func main() {
	planet := flag.String("planet", "mercury",
		"planet name: mercury, venus, earth, mars, jupiter, saturn, uranus, neptune")
	perPhase := flag.Duration("per-phase", 900*time.Millisecond,
		"simulated delay between phase transitions")
	failAt := flag.Int("fail-at", 0,
		"simulate a failure at phase N (1..14); 0 means finish successfully")
	flag.Parse()

	if _, ok := planets.Get(*planet); !ok {
		fmt.Fprintf(os.Stderr, "unknown planet %q; use one of mercury/venus/earth/mars/jupiter/saturn/uranus/neptune\n", *planet)
		os.Exit(2)
	}

	events := make(chan overlay.ProvisionEvent, 16)
	reporter := overlay.NewChannelReporter(events)

	go func() {
		for i, label := range provision.Phases {
			num := i + 1
			reporter.Phase(label, num, len(provision.Phases))
			time.Sleep(*perPhase)
			if *failAt == num {
				reporter.Error(label, errors.New("simulated provisioning failure"))
				close(events)
				return
			}
		}
		reporter.Done()
		close(events)
	}()

	cfg := overlay.ModelConfig{
		Name:   *planet,
		Planet: planets.MustGet(*planet),
		Total:  len(provision.Phases),
		Events: events,
	}
	if err := overlay.Run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "overlay error: %v\n", err)
		os.Exit(1)
	}
}
