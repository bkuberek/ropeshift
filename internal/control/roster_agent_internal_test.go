package control

import (
	"errors"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/roster"
	"ropeshift.com/ropeshift/internal/workorders"
)

// builtinRoster holds no version at either scope, so the built-in roster resolves.
type builtinRoster struct{}

func (builtinRoster) Latest(string) (roster.Version, error) {
	return roster.Version{}, ports.ErrNotFound
}

func (builtinRoster) Version(string, int64) (roster.Version, error) {
	return roster.Version{}, ports.ErrNotFound
}

func (builtinRoster) Versions(string) ([]roster.Version, error) { return nil, nil }

// listedOrders reads only a list of work orders.
type listedOrders struct {
	ports.WorkOrderReader
	orders []workorders.WorkOrder
}

func (l listedOrders) List() ([]workorders.WorkOrder, error) { return l.orders, nil }

// A plan or change work order whose agent would take the shell or file edits from its lane fails the
// definition of ready with a problem that names the agent, as an unknown or disabled agent does, and the
// default plan agent fits.
func TestAnUnfitAgentIsANamedReadyProblem(t *testing.T) {
	for _, order := range []workorders.WorkOrder{
		{ID: "wo-1", Kind: workorders.KindChange, Agent: "reasoning"},
		{ID: "wo-2", Kind: workorders.KindPlan, Agent: "analysis"},
	} {
		problem, err := agentProblem(builtinRoster{}, order)
		if err != nil || !strings.Contains(problem, `"`+order.Agent+`"`) {
			t.Errorf("a %s work order as %s: problem %q, error %v", order.Kind, order.Agent, problem, err)
		}
	}
	if problem, err := agentProblem(builtinRoster{}, workorders.WorkOrder{ID: "wo-3", Kind: workorders.KindPlan}); problem != "" || err != nil {
		t.Errorf("the default plan agent: problem %q, error %v", problem, err)
	}
}

// FR-AGENT-9: no lane runs as the consultant, which only an orchestrator's consultation runs, so work of any kind that
// names it fails the definition of ready with a problem that names the agent.
func TestTheConsultantIsANamedReadyProblemForAnyLane(t *testing.T) {
	for _, kind := range []workorders.Kind{workorders.KindPlan, workorders.KindContractReview, workorders.KindChange, workorders.KindReview} {
		order := workorders.WorkOrder{ID: "wo-1", Kind: kind, Agent: roster.ConsultantAgent}
		problem, err := agentProblem(builtinRoster{}, order)
		if err != nil || !strings.Contains(problem, `"consultant"`) {
			t.Errorf("a %s work order as the consultant: problem %q, error %v", kind, problem, err)
		}
	}
}

// A roster change that strips the shell from an agent an open change work order runs as is refused, naming
// the work order, and one that only a review names is not.
func TestARosterChangeCannotLeaveOpenChangeWorkShellLess(t *testing.T) {
	base := roster.Resolve(roster.Layer{}, roster.Layer{})
	layer, err := roster.Apply(roster.Layer{}, base, roster.Change{Op: roster.OpSet, Agent: "mechanical", Fields: map[string]string{
		"tools.allow": "read,search,edit,context", "tools.deny": "shell,web,delegate",
	}})
	if err != nil {
		t.Fatal(err)
	}
	after := roster.Resolve(layer, roster.Layer{})
	uses, err := openUses(listedOrders{orders: []workorders.WorkOrder{
		{ID: "wo-1", Kind: workorders.KindChange, Agent: "mechanical", State: workorders.Ready},
		{ID: "wo-2", Kind: workorders.KindReview, Agent: "mechanical", State: workorders.Ready},
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = stillUsable(after, uses, "")
	if !errors.Is(err, roster.ErrInUse) || !strings.Contains(err.Error(), "work order wo-1") || strings.Contains(err.Error(), "work order wo-2") {
		t.Errorf("stripping the shell from open change work's agent: %v", err)
	}
}

// Open work that already names an agent unfit for it holds back any roster change, and the refusal names
// that work order and why its agent cannot run it, not the change.
func TestARosterRefusalNamesOpenWorksUnfitAgentAndWhy(t *testing.T) {
	base := roster.Resolve(roster.Layer{}, roster.Layer{})
	layer, err := roster.Apply(roster.Layer{}, base, roster.Change{Op: roster.OpSet, Agent: "mechanical", Fields: map[string]string{"effort": roster.EffortMedium}})
	if err != nil {
		t.Fatal(err)
	}
	uses, err := openUses(listedOrders{orders: []workorders.WorkOrder{
		{ID: "wo-1", Kind: workorders.KindChange, Agent: "reasoning", State: workorders.Ready},
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = stillUsable(roster.Resolve(layer, roster.Layer{}), uses, "")
	if !errors.Is(err, roster.ErrInUse) || !strings.Contains(err.Error(), "work order wo-1") || !strings.Contains(err.Error(), "denies the shell") ||
		strings.Contains(err.Error(), "would leave") {
		t.Errorf("a refusal for open work's unfit agent: %v", err)
	}
}
