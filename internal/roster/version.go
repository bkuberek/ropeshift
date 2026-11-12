package roster

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Version is one version of a scope's layer (FR-AGENT-5). A version never changes once it is written,
// and a revert writes an earlier body as a new one.
type Version struct {
	ID      string `json:"id"`
	Scope   string `json:"scope"`
	Version int64  `json:"version"`
	Digest  string `json:"digest"`
	Body    Layer  `json:"body"`
	// Summary says what the change did, for a person reading the history.
	Summary string `json:"summary"`
	// AuthoredBy is the member whose decision the change is, and PreparedBy the delegate that prepared
	// it, if any.
	AuthoredBy string `json:"authored_by"`
	PreparedBy string `json:"prepared_by,omitempty"`
	CreatedAt  int64  `json:"created_at"`
}

// EventChanged is the event of a new roster version.
const EventChanged = "roster.changed"

// SubjectKind is the event subject kind of a roster scope.
const SubjectKind = "roster"

// Encode is a layer's stored form, and its digest.
func Encode(layer Layer) (string, string, error) {
	raw, err := json.Marshal(layer)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(raw)
	return string(raw), "sha256:" + hex.EncodeToString(sum[:]), nil
}

// Decode reads a layer's stored form.
func Decode(body string) (Layer, error) {
	var layer Layer
	if err := json.Unmarshal([]byte(body), &layer); err != nil {
		return Layer{}, fmt.Errorf("%w: a stored layer: %v", ErrInvalid, err)
	}
	return layer, nil
}

// Summarize says in a line what a change does.
func Summarize(change Change) string {
	switch change.Op {
	case OpSet:
		var parts []string
		for _, field := range slices.Sorted(maps.Keys(change.Fields)) {
			value := change.Fields[field]
			if len(value) > 40 || strings.Contains(value, "\n") {
				value = fmt.Sprintf("(%d characters)", len(value))
			}
			parts = append(parts, field+"="+value)
		}
		return fmt.Sprintf("set %s %s", change.Agent, strings.Join(parts, " "))
	case OpAdd:
		if change.From != "" {
			return fmt.Sprintf("add %s from %s", change.Agent, change.From)
		}
		return fmt.Sprintf("add %s from a file", change.Agent)
	case OpGuideSet:
		return "set the routing guide"
	case OpGuideReset:
		return "reset the routing guide"
	}
	return change.Op + " " + change.Agent
}

// Difference lists what a layer changes from the one before it, one line per field, so a member reads
// each version's difference in the history.
func Difference(before, after Layer) []string {
	var out []string
	names := map[string]bool{}
	for name := range before.Agents {
		names[name] = true
	}
	for name := range after.Agents {
		names[name] = true
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		was, had := before.Agents[name]
		now, has := after.Agents[name]
		switch {
		case !has && was.Added:
			out = append(out, "- "+name+" removed")
		case !has:
			out = append(out, "- "+name+" reset to the layer below")
		case now.Added && !had:
			out = append(out, "+ "+name+" added")
		default:
			out = append(out, fieldDifference(name, fields(was), fields(now))...)
		}
	}
	guide := func(l Layer) string {
		if l.Guide == nil {
			return "(the layer below)"
		}
		return fmt.Sprintf("(%d characters)", len(*l.Guide))
	}
	if (before.Guide == nil) != (after.Guide == nil) || (before.Guide != nil && *before.Guide != *after.Guide) {
		out = append(out, "~ routing guide "+guide(before)+" -> "+guide(after))
	}
	return out
}

func fieldDifference(name string, was, now map[string]string) []string {
	var out []string
	keys := map[string]bool{}
	for key := range was {
		keys[key] = true
	}
	for key := range now {
		keys[key] = true
	}
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		if was[key] != now[key] {
			out = append(out, fmt.Sprintf("~ %s.%s %s -> %s", name, key, shown(was[key]), shown(now[key])))
		}
	}
	return out
}

func shown(value string) string {
	switch {
	case value == "":
		return "(the layer below)"
	case len(value) > 40 || strings.Contains(value, "\n"):
		return fmt.Sprintf("(%d characters)", len(value))
	}
	return value
}

// fields is a patch's set fields as text.
func fields(p Patch) map[string]string {
	out := map[string]string{}
	text := func(field string, value *string) {
		if value != nil {
			out[field] = *value
		}
	}
	text("purpose", p.Purpose)
	text("kind", p.Kind)
	text("access", p.Access)
	text("class", p.Class)
	text("effort", p.Effort)
	text("approval", p.Approval)
	text("instructions", p.Instructions)
	text("report", p.Report)
	if p.Allow != nil {
		out["tools.allow"] = strings.Join(*p.Allow, ",")
	}
	if p.Deny != nil {
		out["tools.deny"] = strings.Join(*p.Deny, ",")
	}
	for driver, model := range p.Models {
		out["models."+driver] = model
	}
	if p.Enabled != nil {
		out["enabled"] = fmt.Sprint(*p.Enabled)
	}
	return out
}
