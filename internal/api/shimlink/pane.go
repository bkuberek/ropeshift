package shimlink

import (
	"encoding/json"
	"fmt"
	"os"
)

// PaneLaunch is what `ropeshift lane-pane` starts. A terminal server's panes inherit the
// server's environment, and anything on a command line can be read by every user of the
// machine, so a session's exact environment and its flags travel in this file, in a
// private directory, and the launcher removes it before it runs anything.
type PaneLaunch struct {
	Dir  string   `json:"dir"`
	Path string   `json:"path"`
	Args []string `json:"args"`
	Env  []string `json:"env"`
}

// WritePaneLaunch keeps a launch for the pane to read.
func WritePaneLaunch(path string, launch PaneLaunch) error {
	raw, err := json.Marshal(launch)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

// TakePaneLaunch reads a launch and removes it, so it is read once.
func TakePaneLaunch(path string) (PaneLaunch, error) {
	var launch PaneLaunch
	raw, err := os.ReadFile(path)
	if err != nil {
		return launch, err
	}
	if err := os.Remove(path); err != nil {
		return launch, err
	}
	if err := json.Unmarshal(raw, &launch); err != nil {
		return launch, fmt.Errorf("%s: %w", path, err)
	}
	return launch, nil
}
