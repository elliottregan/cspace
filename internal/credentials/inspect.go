package credentials

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// inspectRecord is the slice of `container inspect` output this package
// needs. The JSON path is pinned by testdata/inspect-1.2.json, captured
// from a live Apple Container 1.2 host — Apple Container reshapes its
// inspect output across versions (see substrate/applecontainer/adapter.go),
// so a future move fails the fixture test loudly rather than silently
// reporting an empty environment.
type inspectRecord struct {
	Configuration struct {
		InitProcess struct {
			Environment []string `json:"environment"`
		} `json:"initProcess"`
	} `json:"configuration"`
}

// ParseBakedEnv extracts a container's init-process environment from
// `container inspect` output.
//
// Credentials are baked in at create time and immutable for a container's
// life — there is no refresh path — so this, not host re-resolution, is the
// only honest answer to "what credential does this sandbox actually have".
// Reporting host resolution instead is what let doctor show a green check
// while a container held a dead token from an entirely different source.
func ParseBakedEnv(inspectJSON []byte) (map[string]string, error) {
	var records []inspectRecord
	if err := json.Unmarshal(inspectJSON, &records); err != nil {
		// Some paths emit a bare object rather than a single-element array.
		var one inspectRecord
		if err2 := json.Unmarshal(inspectJSON, &one); err2 != nil {
			return nil, fmt.Errorf("parse container inspect: %w", err)
		}
		records = append(records, one)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("container inspect returned no records")
	}
	out := map[string]string{}
	for _, e := range records[0].Configuration.InitProcess.Environment {
		k, v, found := strings.Cut(e, "=")
		if !found {
			continue
		}
		out[k] = v
	}
	return out, nil
}

// Diverged lists cspace-owned keys whose baked value differs from what the
// host resolves now — including keys absent from the container entirely.
//
// Keys cspace no longer resolves are excluded: that is a missing-credential
// report, not a divergence one, and counting it as both would say the same
// thing twice in different words.
func Diverged(baked map[string]string, winners map[string]Credential) []string {
	var out []string
	for _, key := range Keys() {
		w, ok := winners[key]
		if !ok || w.Value == "" {
			continue
		}
		if b, present := baked[key]; !present || b != w.Value {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}
