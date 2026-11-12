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

// The sources of a field: the layer that last set it.
const (
	SourceBuiltin      = "builtin"
	SourceInstallation = "installation"
	SourceProject      = "project"
)

// The scopes a change names. A change never infers its scope, because a change in the wrong scope
// silently changes another project's agents.
const (
	ScopeInstallation = "installation"
	ScopeProject      = "project"
)

// Patch is one layer's change to one agent, field by field. A nil field keeps what the layer below
// holds. Added says the agent is this layer's own, and then every field is set.
type Patch struct {
	Added        bool              `json:"added,omitempty"`
	Purpose      *string           `json:"purpose,omitempty"`
	Kind         *string           `json:"kind,omitempty"`
	Access       *string           `json:"access,omitempty"`
	Allow        *[]string         `json:"allow,omitempty"`
	Deny         *[]string         `json:"deny,omitempty"`
	Class        *string           `json:"class,omitempty"`
	Models       map[string]string `json:"models,omitempty"`
	Effort       *string           `json:"effort,omitempty"`
	Approval     *string           `json:"approval,omitempty"`
	Instructions *string           `json:"instructions,omitempty"`
	Report       *string           `json:"report,omitempty"`
	Enabled      *bool             `json:"enabled,omitempty"`
}

// Layer is one scope's roster: its patches to the agents below it, the agents it adds, and its
// routing guide. It is the body of a roster version.
type Layer struct {
	Agents map[string]Patch `json:"agents,omitempty"`
	Guide  *string          `json:"guide,omitempty"`
}

// ResolvedAgent is one agent of the resolved roster, with the source of each field.
type ResolvedAgent struct {
	Agent
	Sources map[string]string `json:"sources"`
}

// Resolved is the roster a session sees: the built-in roster, then the installation's layer, then a
// project's.
type Resolved struct {
	Agents      []ResolvedAgent `json:"agents,omitempty"`
	Guide       string          `json:"guide"`
	GuideSource string          `json:"guide_source"`
	Defaults    Defaults        `json:"defaults"`
	// Digest names this roster's content, which a lane records with what it started with.
	Digest string `json:"digest"`
}

// Resolve folds the installation's layer and then a project's over the built-in roster.
func Resolve(installation, project Layer) Resolved {
	return base().with(installation, SourceInstallation).with(project, SourceProject)
}

// base is the built-in roster on its own.
func base() Resolved {
	out := Resolved{Guide: Guide, GuideSource: SourceBuiltin, Defaults: BuiltinDefaults()}
	for _, agent := range builtin() {
		out.Agents = append(out.Agents, ResolvedAgent{Agent: clone(agent), Sources: sourcesOf(agent, SourceBuiltin)})
	}
	return out.sealed()
}

// with folds one more layer over a resolved roster.
func (r Resolved) with(layer Layer, source string) Resolved {
	out := Resolved{Guide: r.Guide, GuideSource: r.GuideSource, Defaults: r.Defaults}
	for _, held := range r.Agents {
		agent := ResolvedAgent{Agent: clone(held.Agent), Sources: maps.Clone(held.Sources)}
		if patch, ok := layer.Agents[held.Name]; ok && !patch.Added {
			patch.applyTo(&agent, source)
		}
		out.Agents = append(out.Agents, agent)
	}
	for _, name := range slices.Sorted(maps.Keys(layer.Agents)) {
		patch := layer.Agents[name]
		if !patch.Added || out.has(name) {
			continue
		}
		agent := ResolvedAgent{Agent: Agent{Name: name}, Sources: map[string]string{}}
		patch.applyTo(&agent, source)
		out.Agents = append(out.Agents, agent)
	}
	if layer.Guide != nil {
		out.Guide, out.GuideSource = *layer.Guide, source
	}
	return out.sealed()
}

func (r Resolved) has(name string) bool {
	_, ok := r.Agent(name)
	return ok
}

// Agent returns one agent by name.
func (r Resolved) Agent(name string) (ResolvedAgent, bool) {
	for _, agent := range r.Agents {
		if agent.Name == name {
			return agent, true
		}
	}
	return ResolvedAgent{}, false
}

// Names lists the agents' names in order.
func (r Resolved) Names() []string {
	out := make([]string, 0, len(r.Agents))
	for _, agent := range r.Agents {
		out = append(out, agent.Name)
	}
	return out
}

// sealed sets the digest over everything but the sources, which say where content came from and are
// not content.
func (r Resolved) sealed() Resolved {
	agents := make([]Agent, 0, len(r.Agents))
	for _, agent := range r.Agents {
		agents = append(agents, agent.Agent)
	}
	raw, err := json.Marshal(struct {
		Agents   []Agent  `json:"agents,omitempty"`
		Guide    string   `json:"guide"`
		Defaults Defaults `json:"defaults"`
	}{agents, r.Guide, r.Defaults})
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(raw)
	r.Digest = "sha256:" + hex.EncodeToString(sum[:])
	return r
}

// applyTo sets each field this patch holds, and names its source.
func (p Patch) applyTo(agent *ResolvedAgent, source string) {
	set := func(field string, into *string, value *string) {
		if value != nil {
			*into, agent.Sources[field] = *value, source
		}
	}
	set("purpose", &agent.Purpose, p.Purpose)
	set("kind", &agent.Kind, p.Kind)
	set("access", &agent.Access, p.Access)
	set("class", &agent.Class, p.Class)
	set("effort", &agent.Effort, p.Effort)
	set("approval", &agent.Approval, p.Approval)
	set("instructions", &agent.Instructions, p.Instructions)
	set("report", &agent.Report, p.Report)
	if p.Allow != nil {
		agent.Tools.Allow, agent.Sources["tools.allow"] = slices.Clone(*p.Allow), source
	}
	if p.Deny != nil {
		agent.Tools.Deny, agent.Sources["tools.deny"] = slices.Clone(*p.Deny), source
	}
	for driver, model := range p.Models {
		if agent.Models == nil {
			agent.Models = map[string]string{}
		}
		agent.Models[driver], agent.Sources["models."+driver] = model, source
	}
	if p.Enabled != nil {
		agent.Enabled, agent.Sources["enabled"] = *p.Enabled, source
	}
}

// sourcesOf names one source for every field an agent holds.
func sourcesOf(agent Agent, source string) map[string]string {
	out := map[string]string{}
	for _, field := range []string{"purpose", "kind", "access", "tools.allow", "tools.deny", "class", "effort", "approval", "instructions", "report", "enabled"} {
		out[field] = source
	}
	for driver := range agent.Models {
		out["models."+driver] = source
	}
	return out
}

// whole is the patch that adds an agent: every field set.
func whole(agent Agent) Patch {
	agent = clone(agent)
	return Patch{
		Added: true, Purpose: &agent.Purpose, Kind: &agent.Kind, Access: &agent.Access, Allow: &agent.Tools.Allow, Deny: &agent.Tools.Deny,
		Class: &agent.Class, Models: agent.Models, Effort: &agent.Effort, Approval: &agent.Approval,
		Instructions: &agent.Instructions, Report: &agent.Report, Enabled: &agent.Enabled,
	}
}

func clone(agent Agent) Agent {
	agent.Tools = Tools{Allow: slices.Clone(agent.Tools.Allow), Deny: slices.Clone(agent.Tools.Deny)}
	if agent.Tools.Allow == nil {
		agent.Tools.Allow = []string{}
	}
	if agent.Tools.Deny == nil {
		agent.Tools.Deny = []string{}
	}
	agent.Models = maps.Clone(agent.Models)
	return agent
}

func cloneLayer(layer Layer) Layer {
	out := Layer{Guide: layer.Guide, Agents: map[string]Patch{}}
	maps.Copy(out.Agents, layer.Agents)
	return out
}

// The operations of a change.
const (
	OpSet        = "set"
	OpAdd        = "add"
	OpDisable    = "disable"
	OpEnable     = "enable"
	OpReset      = "reset"
	OpGuideSet   = "guide_set"
	OpGuideReset = "guide_reset"
)

// Change is one change to one scope's layer, as a member asks for it.
type Change struct {
	Op    string `json:"op"`
	Agent string `json:"agent,omitempty"`
	// Fields are `set`'s field and value pairs. A list takes comma-separated categories.
	Fields map[string]string `json:"fields,omitempty"`
	// Definition is an added agent read from a file, and From the agent an added one copies.
	Definition *Agent `json:"definition,omitempty"`
	From       string `json:"from,omitempty"`
	// Guide is the text `guide_set` sets.
	Guide string `json:"guide,omitempty"`
}

// Apply makes one change to a scope's layer. below is the roster the layers under this scope resolve
// to, so a change can read what it changes and the result can be checked whole.
func Apply(layer Layer, below Resolved, change Change) (Layer, error) {
	out := cloneLayer(layer)
	current := below.with(layer, "")
	switch change.Op {
	case OpSet:
		if err := out.set(current, change.Agent, change.Fields); err != nil {
			return Layer{}, err
		}
	case OpAdd:
		if err := out.add(current, change); err != nil {
			return Layer{}, err
		}
	case OpDisable, OpEnable:
		if _, err := held(current, change.Agent); err != nil {
			return Layer{}, err
		}
		enabled := change.Op == OpEnable
		patch := out.Agents[change.Agent]
		patch.Enabled = &enabled
		out.Agents[change.Agent] = patch
	case OpReset:
		if _, ok := out.Agents[change.Agent]; !ok {
			return Layer{}, fmt.Errorf("%w: this scope changes nothing of %q", ErrNothingToReset, change.Agent)
		}
		delete(out.Agents, change.Agent)
	case OpGuideSet:
		if strings.TrimSpace(change.Guide) == "" {
			return Layer{}, fmt.Errorf("%w: an empty routing guide", ErrInvalid)
		}
		guide := change.Guide
		out.Guide = &guide
	case OpGuideReset:
		if out.Guide == nil {
			return Layer{}, fmt.Errorf("%w: this scope holds no routing guide of its own", ErrNothingToReset)
		}
		out.Guide = nil
	default:
		return Layer{}, fmt.Errorf("%w: no operation %q", ErrInvalid, change.Op)
	}
	if len(out.Agents) == 0 {
		out.Agents = nil
	}
	for _, agent := range below.with(out, "").Agents {
		if err := agent.Validate(); err != nil {
			return Layer{}, err
		}
	}
	return out, nil
}

// held returns an agent the roster holds, or why not.
func held(r Resolved, name string) (ResolvedAgent, error) {
	agent, ok := r.Agent(name)
	if !ok {
		return ResolvedAgent{}, fmt.Errorf("%w: %q is in no layer of the roster", ErrUnknownAgent, name)
	}
	return agent, nil
}

// add adds an agent copied from another, or read whole from a definition.
func (l *Layer) add(current Resolved, change Change) error {
	if current.has(change.Agent) {
		return fmt.Errorf("%w: the roster already holds %q", ErrExists, change.Agent)
	}
	var agent Agent
	switch {
	case change.Definition != nil && change.From != "":
		return fmt.Errorf("%w: an agent is added from a file or from another agent, not both", ErrInvalid)
	case change.Definition != nil:
		agent = *change.Definition
	case change.From != "":
		from, err := held(current, change.From)
		if err != nil {
			return err
		}
		agent = from.Agent
	default:
		return fmt.Errorf("%w: an added agent needs a file or an agent to copy", ErrInvalid)
	}
	agent.Name = change.Agent
	if agent.Approval == "" {
		agent.Approval = ApprovalNone
	}
	if len(agent.Tools.Allow) == 0 {
		return fmt.Errorf("%w: agent %q allows no tools", ErrInvalid, agent.Name)
	}
	if err := agent.Validate(); err != nil {
		return err
	}
	l.Agents[change.Agent] = whole(agent)
	return nil
}

// set changes named fields. The values are a person's text, which each field reads in its own way.
func (l *Layer) set(current Resolved, name string, fields map[string]string) error {
	if _, err := held(current, name); err != nil {
		return err
	}
	if len(fields) == 0 {
		return fmt.Errorf("%w: no field to set", ErrInvalid)
	}
	patch := l.Agents[name]
	for _, field := range slices.Sorted(maps.Keys(fields)) {
		value := fields[field]
		text := func(into **string) { *into = &value }
		switch field {
		case "purpose":
			text(&patch.Purpose)
		case "kind":
			text(&patch.Kind)
		case "access":
			text(&patch.Access)
		case "class":
			text(&patch.Class)
		case "effort":
			text(&patch.Effort)
		case "approval":
			text(&patch.Approval)
		case "instructions":
			text(&patch.Instructions)
		case "report":
			text(&patch.Report)
		case "tools.allow":
			list := splitList(value)
			patch.Allow = &list
		case "tools.deny":
			list := splitList(value)
			patch.Deny = &list
		case "enabled":
			enabled, ok := map[string]bool{"true": true, "false": false}[value]
			if !ok {
				return fmt.Errorf("%w: enabled is true or false, not %q", ErrInvalid, value)
			}
			patch.Enabled = &enabled
		default:
			driver, ok := strings.CutPrefix(field, "models.")
			if !ok || driver == "" {
				return fmt.Errorf("%w: no field %q; the fields are purpose, kind, access, tools.allow, tools.deny, class, models.<driver>, effort, approval, instructions, report, and enabled", ErrInvalid, field)
			}
			models := maps.Clone(patch.Models)
			if models == nil {
				models = map[string]string{}
			}
			models[driver] = value
			patch.Models = models
		}
	}
	l.Agents[name] = patch
	return nil
}

// splitList reads comma-separated categories. An empty value is an empty list.
func splitList(value string) []string {
	out := []string{}
	for part := range strings.SplitSeq(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
