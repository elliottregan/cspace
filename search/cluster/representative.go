package cluster

// representative is one candidate point for a path during cluster-
// representative selection. Only one representative per path is handed
// to HDBSCAN so the cluster grain stays at "file = zone member" instead
// of fragmenting into chunks.
type representative struct {
	ID        uint64
	Path      string
	Kind      string
	LineStart int
	Vector    []float32
}

// pickRepresentative returns one representative per path. Prefers
// Kind="file"; if no file-kind point exists for a path, the chunk with
// the lowest LineStart wins.
func pickRepresentative(pts []representative) []representative {
	byPath := map[string][]representative{}
	for _, p := range pts {
		byPath[p.Path] = append(byPath[p.Path], p)
	}
	out := make([]representative, 0, len(byPath))
	for _, group := range byPath {
		var best *representative
		for i := range group {
			if best == nil {
				best = &group[i]
				continue
			}
			if best.Kind != "file" && group[i].Kind == "file" {
				best = &group[i]
				continue
			}
			if best.Kind == group[i].Kind && group[i].LineStart < best.LineStart {
				best = &group[i]
			}
		}
		out = append(out, *best)
	}
	return out
}
