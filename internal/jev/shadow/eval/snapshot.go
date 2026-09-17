// Snapshot loading for the C2 offline evaluator. A snapshot is a read-only
// export of shadow_observations rows; the evaluator never writes one.
package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// Snapshot is a frozen, read-only export of shadow observations. It is the
// recommended reproducible input: the same snapshot file always produces the
// same evaluation result.
type Snapshot struct {
	Source  string        `json:"source"`
	Records []Observation `json:"records"`
}

// LoadSnapshotJSON reads a snapshot JSON file (an array of Observation or a
// Snapshot object) and returns the records. It never mutates the file.
func LoadSnapshotJSON(path string) ([]Observation, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("eval: read snapshot: %w", err)
	}
	// Try a Snapshot object first, then a bare array.
	var snap Snapshot
	if err := json.Unmarshal(b, &snap); err == nil && len(snap.Records) > 0 {
		return snap.Records, nil
	}
	var recs []Observation
	if err := json.Unmarshal(b, &recs); err != nil {
		return nil, fmt.Errorf("eval: parse snapshot: %w", err)
	}
	return recs, nil
}

// WriteSnapshotJSON writes records to a snapshot JSON file. Useful for
// capturing the current store contents (via a read-only dump) so a future
// run can be reproduced exactly. The file is written with 0600 so that a
// snapshot of production observations is not world-readable.
func WriteSnapshotJSON(path string, source string, recs []Observation) error {
	snap := Snapshot{Source: source, Records: recs}
	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("eval: marshal snapshot: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("eval: write snapshot: %w", err)
	}
	return nil
}
