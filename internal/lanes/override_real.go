//go:build realsession

package lanes

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// refusedArguments never reach a session, whatever a suite asks for.
var refusedArguments = []string{
	"--bare", "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions",
	"bypassPermissions", "auth", "setup-token", "respawn", "/login", "/logout",
}

// SessionOverride carries a real-session suite's guard flags to the production
// driver. It exists only under the realsession tag.
type SessionOverride struct {
	Extra  []string `json:"args"`
	Stream string   `json:"stream_log"`
}

// ParseOverride reads a suite's override from one JSON document. A refused argument
// stops the worker from starting a session at all.
func ParseOverride(text string) (SessionOverride, error) {
	if text == "" {
		return SessionOverride{}, nil
	}
	var override SessionOverride
	if err := json.Unmarshal([]byte(text), &override); err != nil {
		return SessionOverride{}, err
	}
	for _, arg := range override.Extra {
		if slices.Contains(refusedArguments, arg) || strings.Contains(arg, "bypassPermissions") {
			return SessionOverride{}, fmt.Errorf("refused argument: %s", arg)
		}
	}
	return override, nil
}

// Args returns the guard flags.
func (o SessionOverride) Args() []string { return o.Extra }

// StreamLog names the file that receives a copy of the raw stream.
func (o SessionOverride) StreamLog() string { return o.Stream }
