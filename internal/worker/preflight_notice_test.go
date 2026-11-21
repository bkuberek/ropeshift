package worker_test

import (
	"context"
	"slices"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// noticing is a driver whose preflight says it left a piece of the session's setup out.
type noticing struct{ ports.AgentDriver }

// leftOut is what noticing's preflight says.
const leftOut = "the repository's .mcp.json names an MCP server \"ropeshift\", so it is left out"

func (d noticing) Preflight(ctx context.Context, spec ports.SessionSpec) (ports.PreflightReport, error) {
	report, err := d.AgentDriver.Preflight(ctx, spec)
	report.Notices = append(report.Notices, leftOut)
	return report, err
}

// What a driver's preflight says it left out of a session's setup reaches a person as a notice naming the lane, and
// the lane starts as it would have.
func TestWhatAPreflightLeftOutIsANotice(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.config.Driver = noticing{AgentDriver: r.driver}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.1)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := r.row(); row.State != workorders.Acceptance {
		t.Fatalf("row %+v", row)
	}
	if notices := r.notices(); !slices.Contains(notices, "lane lane-1: "+leftOut) {
		t.Errorf("the worker said %q", notices)
	}
}
