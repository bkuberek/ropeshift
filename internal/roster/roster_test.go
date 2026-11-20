package roster

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestTheBuiltInRosterHoldsEveryKindOfWork is FR-AGENT-1: the ten agents of ADR-0025, each valid,
// each denying delegate, and each purpose naming the neighbor to choose instead.
func TestTheBuiltInRosterHoldsEveryKindOfWork(t *testing.T) {
	want := []string{"lookup", "explore", "analysis", "reasoning", "mechanical", "light-implementation", "implementation", "heavy-implementation", "deep-implementation", "review"}
	resolved := Resolve(Layer{}, Layer{})
	if got := resolved.Names(); !slices.Equal(got, want) {
		t.Fatalf("names %q", got)
	}
	perUse := map[string]bool{"reasoning": true, "deep-implementation": true}
	for _, agent := range resolved.Agents {
		if err := agent.Validate(); err != nil {
			t.Errorf("%s: %v", agent.Name, err)
		}
		if !slices.Contains(agent.Tools.Deny, ToolDelegate) {
			t.Errorf("%s does not deny delegate", agent.Name)
		}
		if (agent.Approval == ApprovalPerUse) != perUse[agent.Name] {
			t.Errorf("%s approval %s", agent.Name, agent.Approval)
		}
		if agent.Report == "" || !agent.Enabled {
			t.Errorf("%s: %+v", agent.Name, agent.Agent)
		}
		for field, source := range agent.Sources {
			if source != SourceBuiltin {
				t.Errorf("%s.%s from %s", agent.Name, field, source)
			}
		}
	}
	if resolved.Guide == "" || resolved.GuideSource != SourceBuiltin || resolved.Digest == "" {
		t.Errorf("guide %q from %s, digest %q", resolved.Guide, resolved.GuideSource, resolved.Digest)
	}
}

func TestAnAgentDefinitionIsRefusedWhenItBreaksTheFormat(t *testing.T) {
	good := builtin()[0]
	for _, tt := range []struct {
		name   string
		change func(*Agent)
		want   string
	}{
		{"name", func(a *Agent) { a.Name = "Bad_Name" }, "name"},
		{"kind", func(a *Agent) { a.Kind = "chat" }, "kind"},
		{"class", func(a *Agent) { a.Class = "huge" }, "class"},
		{"effort", func(a *Agent) { a.Effort = "extreme" }, "effort"},
		{"approval", func(a *Agent) { a.Approval = "sometimes" }, "approval"},
		{"tool", func(a *Agent) { a.Tools.Allow = append(a.Tools.Allow, "teleport") }, "tool"},
		{"overlap", func(a *Agent) { a.Tools.Deny = append(a.Tools.Deny, a.Tools.Allow[0]) }, "both"},
		{"read-only edit", func(a *Agent) { a.Tools.Allow, a.Tools.Deny = []string{ToolRead, ToolEdit}, []string{ToolDelegate} }, "read-only"},
		{"purpose", func(a *Agent) { a.Purpose = " " }, "purpose"},
		{"instructions", func(a *Agent) { a.Instructions = "" }, "instructions"},
		{"model", func(a *Agent) { a.Models = map[string]string{"claude-code": ""} }, "model"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			agent := good
			agent.Tools = Tools{Allow: slices.Clone(good.Tools.Allow), Deny: slices.Clone(good.Tools.Deny)}
			tt.change(&agent)
			err := agent.Validate()
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want an invalid %s", err, tt.want)
			}
		})
	}
}

// TestTheRosterResolvesFieldByFieldWithEachSource is FR-AGENT-4: the installation's layer, then the
// project's, field by field, with each field's source.
func TestTheRosterResolvesFieldByFieldWithEachSource(t *testing.T) {
	installation := mustApply(t, Layer{}, Resolve(Layer{}, Layer{}), Change{Op: OpSet, Agent: "explore", Fields: map[string]string{
		"purpose": "Search widely.", "class": ClassStandard, "models.claude-code": "claude-sonnet-5-5",
	}})
	below := Resolve(installation, Layer{})
	project := mustApply(t, Layer{}, below, Change{Op: OpSet, Agent: "explore", Fields: map[string]string{"class": ClassLarge, "tools.allow": "read,search"}})
	resolved := Resolve(installation, project)
	explore, ok := resolved.Agent("explore")
	if !ok {
		t.Fatal("no explore")
	}
	if explore.Purpose != "Search widely." || explore.Class != ClassLarge || explore.Models["claude-code"] != "claude-sonnet-5-5" ||
		!slices.Equal(explore.Tools.Allow, []string{ToolRead, ToolSearch}) {
		t.Fatalf("explore %+v", explore.Agent)
	}
	for field, want := range map[string]string{
		"purpose": SourceInstallation, "class": SourceProject, "models.claude-code": SourceInstallation,
		"tools.allow": SourceProject, "tools.deny": SourceBuiltin, "instructions": SourceBuiltin,
	} {
		if explore.Sources[field] != want {
			t.Errorf("%s from %q, want %q", field, explore.Sources[field], want)
		}
	}
	if resolved.Digest == Resolve(installation, Layer{}).Digest {
		t.Error("a project's change left the digest as it was")
	}
}

func TestChangesAddDisableResetAndEditTheGuide(t *testing.T) {
	base := Resolve(Layer{}, Layer{})
	layer := mustApply(t, Layer{}, base, Change{Op: OpAdd, Agent: "docs-writer", From: "reasoning"})
	layer = mustApply(t, layer, base, Change{Op: OpSet, Agent: "docs-writer", Fields: map[string]string{"approval": ApprovalNone}})
	layer = mustApply(t, layer, base, Change{Op: OpDisable, Agent: "lookup"})
	layer = mustApply(t, layer, base, Change{Op: OpGuideSet, Guide: "Choose wisely."})
	resolved := Resolve(layer, Layer{})
	added, ok := resolved.Agent("docs-writer")
	if !ok || added.Kind != KindReasoning || added.Approval != ApprovalNone || added.Sources["kind"] != SourceInstallation {
		t.Fatalf("added %+v", added)
	}
	if lookup, _ := resolved.Agent("lookup"); lookup.Enabled || lookup.Sources["enabled"] != SourceInstallation {
		t.Errorf("lookup %+v", lookup)
	}
	if resolved.Guide != "Choose wisely." || resolved.GuideSource != SourceInstallation {
		t.Errorf("guide %q from %s", resolved.Guide, resolved.GuideSource)
	}

	layer = mustApply(t, layer, base, Change{Op: OpReset, Agent: "docs-writer"})
	layer = mustApply(t, layer, base, Change{Op: OpReset, Agent: "lookup"})
	layer = mustApply(t, layer, base, Change{Op: OpGuideReset})
	if again := Resolve(layer, Layer{}); again.Digest != base.Digest {
		t.Errorf("a reset of everything left %+v", layer)
	}
	for _, tt := range []struct {
		change Change
		want   error
	}{
		{Change{Op: OpAdd, Agent: "review", From: "analysis"}, ErrExists},
		{Change{Op: OpSet, Agent: "nobody", Fields: map[string]string{"class": ClassSmall}}, ErrUnknownAgent},
		{Change{Op: OpSet, Agent: "explore", Fields: map[string]string{"colour": "blue"}}, ErrInvalid},
		{Change{Op: OpSet, Agent: "explore", Fields: map[string]string{"tools.allow": "read,edit"}}, ErrInvalid},
		{Change{Op: OpReset, Agent: "explore"}, ErrNothingToReset},
		{Change{Op: "rename"}, ErrInvalid},
	} {
		if _, err := Apply(Layer{}, base, tt.change); !errors.Is(err, tt.want) {
			t.Errorf("%+v: got %v, want %v", tt.change, err, tt.want)
		}
	}
}

// TestAnAddedDefinitionIsSummarizedByWhereItWasRead holds the history's line for an added agent to where its
// definition came from: a file, text pasted in the web UI, another agent, or a definition that says neither.
func TestAnAddedDefinitionIsSummarizedByWhereItWasRead(t *testing.T) {
	definition := &Agent{Name: "scout", Purpose: "Finds the code a change touches.", Kind: KindLookup, Access: AccessReadOnly, Class: ClassSmall, Effort: "low", Enabled: true}
	for _, tt := range []struct {
		change Change
		want   string
	}{
		{Change{Op: OpAdd, Agent: "scout", Definition: definition, DefinitionFrom: DefinitionFile}, "add scout from a file"},
		{Change{Op: OpAdd, Agent: "scout", Definition: definition, DefinitionFrom: DefinitionPasted}, "add scout from a pasted definition"},
		{Change{Op: OpAdd, Agent: "scout", Definition: definition}, "add scout from a definition"},
		{Change{Op: OpAdd, Agent: "scout", From: "explore"}, "add scout from explore"},
	} {
		if got := Summarize(tt.change); got != tt.want {
			t.Errorf("%+v: %q, want %q", tt.change, got, tt.want)
		}
	}
	base := Resolve(Layer{}, Layer{})
	for _, change := range []Change{
		{Op: OpAdd, Agent: "scout", Definition: definition, DefinitionFrom: "clipboard"},
		{Op: OpAdd, Agent: "scout", From: "explore", DefinitionFrom: DefinitionPasted},
	} {
		if _, err := Apply(Layer{}, base, change); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: got %v, want %v", change, err, ErrInvalid)
		}
	}
}

// TestTheDefaultMappingChoosesEachLanesAgent is FR-AGENT-13: by kind and size, a named agent first,
// rework one implementation tier up and never into a per-use agent, and an unknown or disabled agent
// refused by name.
func TestTheDefaultMappingChoosesEachLanesAgent(t *testing.T) {
	resolved := Resolve(Layer{}, Layer{})
	for _, tt := range []struct {
		work LaneWork
		want string
	}{
		{LaneWork{Kind: "plan"}, "heavy-implementation"},
		{LaneWork{Kind: "contract_review"}, "analysis"},
		{LaneWork{Kind: "review", Size: "large"}, "review"},
		{LaneWork{Kind: "change", Size: "small"}, "light-implementation"},
		{LaneWork{Kind: "change"}, "implementation"},
		{LaneWork{Kind: "change", Size: "unknown"}, "implementation"},
		{LaneWork{Kind: "change", Size: "large"}, "heavy-implementation"},
		{LaneWork{Kind: "change", Size: "small", Named: "deep-implementation"}, "deep-implementation"},
		{LaneWork{Kind: "rework", Size: "small", Reworks: "light-implementation"}, "implementation"},
		{LaneWork{Kind: "rework", Reworks: "implementation"}, "heavy-implementation"},
		{LaneWork{Kind: "rework", Reworks: "heavy-implementation"}, "heavy-implementation"},
		{LaneWork{Kind: "change", Reworks: "deep-implementation"}, "deep-implementation"},
		{LaneWork{Kind: "rework", Size: "large"}, "heavy-implementation"},
	} {
		got, err := resolved.LaneAgent(tt.work)
		if err != nil || got.Name != tt.want {
			t.Errorf("%+v: got %q, %v, want %q", tt.work, got.Name, err, tt.want)
		}
	}
	disabled := Resolve(mustApply(t, Layer{}, resolved, Change{Op: OpDisable, Agent: "mechanical"}), Layer{})
	if _, err := disabled.LaneAgent(LaneWork{Kind: "change", Named: "mechanical"}); !errors.Is(err, ErrDisabledAgent) || !strings.Contains(err.Error(), "mechanical") {
		t.Errorf("a disabled agent: %v", err)
	}
	if _, err := resolved.LaneAgent(LaneWork{Kind: "change", Named: "ghost"}); !errors.Is(err, ErrUnknownAgent) || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("an unknown agent: %v", err)
	}
}

// TestAPlanOrChangeLanesAgentKeepsItsShellAndEdits: a lane's own agent narrows the lane's rules by what it
// denies (ADR-0025), and plan, change, and rework lanes must write and commit, so an agent that denies the
// shell or file edits is refused for them by name, whether the work order or the default mapping names it.
// Contract review and review lanes may run as such an agent.
func TestAPlanOrChangeLanesAgentKeepsItsShellAndEdits(t *testing.T) {
	resolved := Resolve(Layer{}, Layer{})
	for _, kind := range []string{"plan", "change", "rework"} {
		for agent, denied := range map[string]string{"reasoning": "shell", "analysis": "file edits", "explore": "shell and file edits"} {
			_, err := resolved.LaneAgent(LaneWork{Kind: kind, Named: agent})
			if !errors.Is(err, ErrUnfitAgent) || !strings.Contains(err.Error(), `"`+agent+`"`) || !strings.Contains(err.Error(), denied) {
				t.Errorf("a %s lane as %s: %v", kind, agent, err)
			}
		}
	}
	for _, kind := range []string{"contract_review", "review"} {
		if _, err := resolved.LaneAgent(LaneWork{Kind: kind, Named: "analysis"}); err != nil {
			t.Errorf("a %s lane as analysis: %v", kind, err)
		}
	}
	if missing := resolved.Missing(resolved.DefaultUses()); len(missing) != 0 {
		t.Errorf("the built-in default mapping names an agent its work cannot use: %+v", missing)
	}
	shellLess := Resolve(mustApply(t, Layer{}, resolved, Change{Op: OpSet, Agent: "heavy-implementation", Fields: map[string]string{
		"tools.allow": "read,search,edit,web,context", "tools.deny": "shell,delegate",
	}}), Layer{})
	if _, err := shellLess.LaneAgent(LaneWork{Kind: "plan"}); !errors.Is(err, ErrUnfitAgent) || !strings.Contains(err.Error(), "heavy-implementation") {
		t.Errorf("a plan lane by default as a shell-less agent: %v", err)
	}
}

// TestAnAgentInUseCannotBeDisabledOrRemoved is FR-AGENT-4: the refusal names what still uses it.
func TestAnAgentInUseCannotBeDisabledOrRemoved(t *testing.T) {
	base := Resolve(Layer{}, Layer{})
	off := Resolve(mustApply(t, Layer{}, base, Change{Op: OpDisable, Agent: "implementation"}), Layer{})
	missing := off.Missing(base.DefaultUses())
	if len(missing) != 1 || missing[0].Agent != "implementation" || !strings.Contains(missing[0].By, "default mapping") {
		t.Fatalf("missing %+v", missing)
	}
	added := mustApply(t, Layer{}, base, Change{Op: OpAdd, Agent: "porter", From: "mechanical"})
	gone := Resolve(mustApply(t, added, base, Change{Op: OpReset, Agent: "porter"}), Layer{})
	uses := []Use{{Agent: "porter", By: "work order w-1"}}
	if missing := gone.Missing(uses); len(missing) != 1 || missing[0].By != "work order w-1" {
		t.Errorf("missing %+v", missing)
	}
	if missing := Resolve(added, Layer{}).Missing(uses); len(missing) != 0 {
		t.Errorf("an enabled agent counted as missing: %+v", missing)
	}
	// A change that leaves a plan or change lane's agent denying the shell is refused like a disable.
	stripped := Resolve(mustApply(t, Layer{}, base, Change{Op: OpSet, Agent: "implementation", Fields: map[string]string{
		"tools.allow": "read,search,edit,web,context", "tools.deny": "shell,delegate",
	}}), Layer{})
	if missing := stripped.Missing(base.DefaultUses()); len(missing) != 1 || missing[0].Agent != "implementation" || missing[0].Kind != "change" {
		t.Errorf("the default mapping's shell-less agent: missing %+v", missing)
	}
	open := []Use{{Agent: "implementation", By: "work order w-2", Kind: "change"}, {Agent: "implementation", By: "work order w-3", Kind: "review"}}
	if missing := stripped.Missing(open); len(missing) != 1 || missing[0].By != "work order w-2" {
		t.Errorf("open work's shell-less agent: missing %+v", missing)
	}
}

func mustApply(t *testing.T, layer Layer, below Resolved, change Change) Layer {
	t.Helper()
	out, err := Apply(layer, below, change)
	if err != nil {
		t.Fatalf("%+v: %v", change, err)
	}
	return out
}
