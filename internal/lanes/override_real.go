//go:build realsession

package lanes

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ReadsVendorStatus is false here, so a build that runs real sessions never runs a sign-in
// command in any form, whatever a test does. Other sessions share this login.
const ReadsVendorStatus = false

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
	// Session lists variables a session starts with. A pane has no flag that keeps a
	// transcript out of a person's home directory, and this is the documented way.
	Session []string `json:"env"`
	// ExpiredCache has the resume table read every cache as past its lifetime, so a suite can
	// walk the fresh path with no long park.
	ExpiredCache bool `json:"expired_cache"`
}

// allowedVariables are all a suite may add to a session.
var allowedVariables = []string{"CLAUDE_CODE_SKIP_PROMPT_HISTORY=1"}

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
	for _, entry := range override.Session {
		if !slices.Contains(allowedVariables, entry) {
			return SessionOverride{}, fmt.Errorf("refused variable: %s", entry)
		}
	}
	return override, nil
}

// Args returns the guard flags.
func (o SessionOverride) Args() []string { return o.Extra }

// Env returns the variables a suite adds to a session.
func (o SessionOverride) Env() []string { return o.Session }

// StreamLog names the file that receives a copy of the raw stream.
func (o SessionOverride) StreamLog() string { return o.Stream }

// CacheLifetime reads a driver's lifetime, or none for a suite that walks the fresh path.
func (o SessionOverride) CacheLifetime(documented time.Duration) time.Duration {
	if o.ExpiredCache {
		return 0
	}
	return documented
}
