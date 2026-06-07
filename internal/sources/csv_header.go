package sources

import (
	"fmt"
	"strings"
)

// indexHeader maps each required column name to its 0-based position in the
// given CSV header row. It returns an error if any required column is missing
// so loaders fail fast on schema drift instead of silently producing wrong
// records.
func indexHeader(header, required []string) (map[string]int, error) {
	positions := make(map[string]int, len(header))
	for i, name := range header {
		positions[strings.ToLower(strings.TrimSpace(name))] = i
	}

	cols := make(map[string]int, len(required))
	for _, name := range required {
		idx, ok := positions[name]
		if !ok {
			return nil, fmt.Errorf("missing required column %q in header %v", name, header)
		}
		cols[name] = idx
	}
	return cols, nil
}
