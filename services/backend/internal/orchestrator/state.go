package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State is the orchestrator progress persisted to disk so a restart resumes the timeline.
type State struct {
	Running       bool      `json:"running"`
	CaughtUp      bool      `json:"caughtUp"`
	SimulatedTime time.Time `json:"simulatedTime"`
	Ticks         int64     `json:"ticks"`
}

func loadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("reading state file: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("parsing state file: %w", err)
	}
	return state, nil
}

// saveState writes via a temp file and rename so a crash never leaves a half-written file.
func saveState(path string, state State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("writing state file: %w", err)
	}
	return os.Rename(tmp, path)
}
