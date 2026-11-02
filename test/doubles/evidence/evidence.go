// Package evidence keeps what a real session did, for whoever reads a run that stopped. A run's
// report keeps kinds and names and no text, which is right for what a test prints and leaves nothing
// to read when a session did the wrong thing. This keeps the text, in a log nothing commits, with
// nothing in it that names a session or an account.
package evidence

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// What stands where something private stood.
const (
	SessionMark = "<session>"
	AccountMark = "<account>"
	HomeMark    = "<home>"
)

// keptFields are all a hook's input leaves in the log, by name, so a field a later CLI adds stays out
// until someone reads what it holds.
var keptFields = []string{
	"hook_event_name", "permission_mode", "tool_name", "tool_input", "tool_response", "prompt", "last_assistant_message",
	"message", "notification_type", "source", "reason", "trigger", "error", "model",
}

var (
	sessionForm = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	accountForm = regexp.MustCompile(`[\pL\pN._%+-]+@[\pL\pN-]+\.[\pL\pN.-]+`)
)

// Tags say where one hook ran, from the session's own environment.
type Tags struct {
	Lane       string
	Generation string
	Home       string
}

// Scrub takes out what names a session or an account, wherever it stands in a text.
func Scrub(text, home string) string {
	if home != "" {
		text = strings.ReplaceAll(text, home, HomeMark)
	}
	text = sessionForm.ReplaceAllString(text, SessionMark)
	return accountForm.ReplaceAllString(text, AccountMark)
}

// HookLine is one line of the log, for one hook. Input it cannot read says so and keeps nothing,
// because what it could not read it could not scrub.
func HookLine(input []byte, tags Tags, at time.Time) []byte {
	line := map[string]any{"at_ms": at.UnixMilli(), "lane": tags.Lane, "generation": tags.Generation}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(input, &payload); err != nil {
		line["malformed"] = true
		payload = nil
	}
	for _, name := range keptFields {
		if value, ok := payload[name]; ok {
			line[name] = value
		}
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(line); err != nil {
		return []byte(`{"unwritable":true,"at_ms":` + strconv.FormatInt(at.UnixMilli(), 10) + "}\n")
	}
	return []byte(Scrub(out.String(), tags.Home))
}
