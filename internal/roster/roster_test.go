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

// TestTheDefaultMappingChoosesEachLanesAgent is FR-AGENT-13: by kind and size, a named agent first,
// rework one implementation tier up and never into a per-use agent, and an unknown or disabled agent
// refused by name.
func TestTheDefaultMappingChoosesEachLanesAgent(t *testing.T) {
	resolved := Resolve(Layer{}, Layer{})
	for _, tt := range []struct {
		work LaneWork
		want string
	}{
		{LaneWork{Kind: "plan"}, "reasoning"},
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
}

func mustApply(t *testing.T, layer Layer, below Resolved, change Change) Layer {
	t.Helper()
	out, err := Apply(layer, below, change)
	if err != nil {
		t.Fatalf("%+v: %v", change, err)
	}
	return out
}
