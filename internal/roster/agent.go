package roster

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The kinds of work an agent does.
const (
	KindLookup         = "lookup"
	KindExplore        = "explore"
	KindAnalysis       = "analysis"
	KindReasoning      = "reasoning"
	KindMechanical     = "mechanical"
	KindImplementation = "implementation"
	KindReview         = "review"
)

var kinds = []string{KindLookup, KindExplore, KindAnalysis, KindReasoning, KindMechanical, KindImplementation, KindReview}

// An agent's access. A read-only agent is always rendered with edit denied, as a guardrail.
const (
	AccessReadOnly = "read_only"
	AccessWriting  = "writing"
)

// The capability classes, from the least capable and costly to the most. Each driver profile maps
// every class to one of its models (`[agent.classes]`).
const (
	ClassSmall    = "small"
	ClassStandard = "standard"
	ClassLarge    = "large"
	ClassFrontier = "frontier"
)

// Classes lists the classes in rank order.
var Classes = []string{ClassSmall, ClassStandard, ClassLarge, ClassFrontier}

// The efforts, which each driver maps to its own setting, in rank order.
const (
	EffortLow    = "low"
	EffortMedium = "medium"
	EffortHigh   = "high"
	EffortMax    = "max"
)

var efforts = []string{EffortLow, EffortMedium, EffortHigh, EffortMax}

// An agent's approval. A per-use agent waits for an approval on each start.
const (
	ApprovalNone   = "none"
	ApprovalPerUse = "per_use"
)

// The driver-neutral tool categories. Context is ropeshift's own context tools, and delegate is
// starting another agent.
const (
	ToolRead     = "read"
	ToolSearch   = "search"
	ToolEdit     = "edit"
	ToolShell    = "shell"
	ToolWeb      = "web"
	ToolContext  = "context"
	ToolDelegate = "delegate"
)

// ToolCategories lists every category, in the order a rendering lists them.
var ToolCategories = []string{ToolRead, ToolSearch, ToolEdit, ToolShell, ToolWeb, ToolContext, ToolDelegate}

// The errors of the roster. Each wraps the detail a person reads.
var (
	// ErrInvalid marks a definition or a change the format refuses.
	ErrInvalid = errors.New("invalid roster")
	// ErrUnknownAgent marks a name the resolved roster does not hold.
	ErrUnknownAgent = errors.New("unknown agent")
	// ErrDisabledAgent marks an agent the resolved roster holds disabled.
	ErrDisabledAgent = errors.New("disabled agent")
	// ErrExists marks an added agent whose name the roster already holds.
	ErrExists = errors.New("agent exists")
	// ErrNothingToReset marks a reset at a scope that changed nothing of the agent.
	ErrNothingToReset = errors.New("nothing to reset")
	// ErrInUse marks a disable or a removal of an agent something still names.
	ErrInUse = errors.New("agent in use")
)

// Tools are an agent's allowed and denied tool categories.
type Tools struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

// Agent is one agent definition.
type Agent struct {
	Name string `json:"name"`
	// Purpose says when to use this agent, and when to choose another. A driver renders it as the
	// agent's description, which the coordinating session reads when it picks one.
	Purpose string `json:"purpose"`
	Kind    string `json:"kind"`
	Access  string `json:"access"`
	Tools   Tools  `json:"tools"`
	Class   string `json:"class"`
	// Models is a concrete model per driver, which overrides the class for that driver.
	Models   map[string]string `json:"models,omitempty"`
	Effort   string            `json:"effort"`
	Approval string            `json:"approval"`
	// Instructions are the agent's instruction body, and Report the format it reports in.
	Instructions string `json:"instructions"`
	Report       string `json:"report"`
	Enabled      bool   `json:"enabled"`
}

// slug is a lowercase, hyphenated name.
var slug = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// maxName bounds a name, well inside what every driver takes.
const maxName = 64

// Validate checks a whole definition.
func (a Agent) Validate() error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf("%w: agent %q: %s", ErrInvalid, a.Name, fmt.Sprintf(format, args...))
	}
	switch {
	case !slug.MatchString(a.Name) || len(a.Name) > maxName:
		return invalid("a name is a lowercase, hyphenated slug of at most %d characters", maxName)
	case strings.TrimSpace(a.Purpose) == "":
		return invalid("no purpose")
	case strings.TrimSpace(a.Instructions) == "":
		return invalid("no instructions")
	case !slices.Contains(kinds, a.Kind):
		return invalid("kind %q is none of %s", a.Kind, strings.Join(kinds, ", "))
	case a.Access != AccessReadOnly && a.Access != AccessWriting:
		return invalid("access %q is neither %s nor %s", a.Access, AccessReadOnly, AccessWriting)
	case !slices.Contains(Classes, a.Class):
		return invalid("class %q is none of %s", a.Class, strings.Join(Classes, ", "))
	case !slices.Contains(efforts, a.Effort):
		return invalid("effort %q is none of %s", a.Effort, strings.Join(efforts, ", "))
	case a.Approval != ApprovalNone && a.Approval != ApprovalPerUse:
		return invalid("approval %q is neither %s nor %s", a.Approval, ApprovalNone, ApprovalPerUse)
	}
	for _, tool := range slices.Concat(a.Tools.Allow, a.Tools.Deny) {
		if !slices.Contains(ToolCategories, tool) {
			return invalid("tool %q is none of %s", tool, strings.Join(ToolCategories, ", "))
		}
		if slices.Contains(a.Tools.Allow, tool) && slices.Contains(a.Tools.Deny, tool) {
			return invalid("tool %q is both allowed and denied", tool)
		}
	}
	if a.Access == AccessReadOnly && slices.Contains(a.Tools.Allow, ToolEdit) {
		return invalid("a read-only agent cannot allow %s", ToolEdit)
	}
	for driver, model := range a.Models {
		if !slug.MatchString(driver) || strings.TrimSpace(model) == "" {
			return invalid("model %q for driver %q", model, driver)
		}
	}
	return nil
}

// ModelFor is the model this agent runs on a driver: its own model for that driver, or the one the
// driver's profile maps its class to. Empty means the profile maps none.
func (a Agent) ModelFor(driver string, classes map[string]string) string {
	if model := a.Models[driver]; model != "" {
		return model
	}
	return classes[a.Class]
}

// rank is a value's place in an ordered list, and -1 for one it does not hold.
func rank(list []string, value string) int { return slices.Index(list, value) }
