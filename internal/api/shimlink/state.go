package shimlink

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Config is one shim.
type Config struct {
	// Dir holds the journal, the state file, and the lock.
	Dir string
	// Socket is where clients reach this shim.
	Socket string
	// Command is the child and its arguments.
	Command []string
	// Linger is how long the shim stays after the child ends. Zero takes the default.
	Linger time.Duration
	// JournalCap bounds the journal on disk. Zero takes the default.
	JournalCap int64
}

// DefaultLinger is how long a shim stays after its child ends, so a worker that is coming
// back can read the end through the socket. The journal keeps it after that.
const DefaultLinger = time.Minute

const stateFile = "state.json"

// ExitLocked is the exit code of a shim that found its generation held.
const ExitLocked = 17

// State is what a returning worker reads when no shim answers.
type State struct {
	Version  int `json:"version"`
	ShimPID  int `json:"shim_pid"`
	ChildPID int `json:"child_pid,omitempty"`
}

func WriteState(dir string, state State) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	next := filepath.Join(dir, stateFile+".next")
	if err := os.WriteFile(next, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(next, filepath.Join(dir, stateFile))
}

// ReadState reads a generation's state, or fails with os.ErrNotExist if no shim got as
// far as writing it.
func ReadState(dir string) (State, error) {
	raw, err := os.ReadFile(filepath.Join(dir, stateFile))
	if err != nil {
		return State{}, err
	}
	var state State
	err = json.Unmarshal(raw, &state)
	return state, err
}

// Args is a shim's command line, so the driver cannot drift from Main.
func Args(config Config) []string {
	args := []string{"lane-shim", "--dir", config.Dir, "--socket", config.Socket}
	if config.Linger != 0 {
		args = append(args, "--linger", config.Linger.String())
	}
	if config.JournalCap != 0 {
		args = append(args, "--journal-cap", strconv.FormatInt(config.JournalCap, 10))
	}
	return append(append(args, "--"), config.Command...)
}
