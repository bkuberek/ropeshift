package server_test

import (
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

func laneEnd(checks ...workorders.CheckResult) workorders.LaneResult {
	return workorders.LaneResult{
		Report: workorders.Report{Outcome: workorders.OutcomeCompleted, Artifacts: []string{"notes.md"}, Checks: checks},
		Branch: "lane/wo", Head: "abc123",
	}
}

func TestAMemberAcceptsWhatOnlyItsLaneVouchesFor(t *testing.T) {
	d := apitest.Start(t)
	d.Completed(t, "wo", laneEnd(workorders.CheckResult{Name: "unit", Criterion: "c1", Passed: true}))
	view, err := d.Client.Acceptance(t.Context(), "wo")
	if err != nil {
		t.Fatal(err)
	}
	if view.State != workorders.Acceptance || view.Assessment.Needs != workorders.NeedSecondPrincipal {
		t.Errorf("standing %+v", view)
	}
	if len(view.Checks) != 1 || view.Checks[0].Source != workorders.SourceLane || len(view.Artifacts) != 2 {
		t.Errorf("linked: %+v, %+v", view.Checks, view.Artifacts)
	}
	accepted, err := d.Client.Accept(t.Context(), "wo")
	if err != nil {
		t.Fatal(err)
	}
	if accepted.State != workorders.Done {
		t.Errorf("accepted into %s", accepted.State)
	}
	row, err := d.Client.WorkOrder(t.Context(), "wo")
	if err != nil {
		t.Fatal(err)
	}
	if row.State != workorders.Done {
		t.Errorf("the record reads %s", row.State)
	}
}

func TestNoEvidenceReadsAsEmptyLists(t *testing.T) {
	d := apitest.Start(t)
	d.Completed(t, "wo", workorders.LaneResult{Report: workorders.Report{Outcome: workorders.OutcomeCompleted}})
	view, err := d.Client.Acceptance(t.Context(), "wo")
	if err != nil {
		t.Fatal(err)
	}
	if view.Artifacts == nil || view.Checks == nil {
		t.Errorf("an empty list must read as a list: %+v", view)
	}
}

func TestAcceptanceRefusals(t *testing.T) {
	failed := workorders.CheckResult{Name: "unit", Criterion: "c1", Passed: false}
	tests := []struct {
		name   string
		end    workorders.LaneResult
		call   func(*testing.T, *api.ProjectClient) error
		status int
		code   string
	}{
		{
			name: "a failed check", end: laneEnd(failed), status: http.StatusConflict, code: api.CodeNotAcceptable,
			call: func(t *testing.T, c *api.ProjectClient) error { _, err := c.Accept(t.Context(), "wo"); return err },
		},
		{
			name: "a rework with no reason", end: laneEnd(), status: http.StatusBadRequest, code: api.CodeInvalid,
			call: func(t *testing.T, c *api.ProjectClient) error {
				_, err := c.Rework(t.Context(), "wo", api.ReworkRequest{})
				return err
			},
		},
		{
			name: "an absent work order", end: laneEnd(), status: http.StatusNotFound, code: api.CodeNotFound,
			call: func(t *testing.T, c *api.ProjectClient) error {
				_, err := c.Acceptance(t.Context(), "absent")
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := apitest.Start(t)
			d.Completed(t, "wo", tt.end)
			wantRefused(t, tt.call(t, d.Client), tt.status, tt.code)
		})
	}
}

func TestAReworkLeavesAcceptanceOnce(t *testing.T) {
	d := apitest.Start(t)
	d.Completed(t, "wo", laneEnd())
	first, err := d.Client.Rework(t.Context(), "wo", api.ReworkRequest{Reason: "the retries do not stop"})
	if err != nil {
		t.Fatal(err)
	}
	if first.State != workorders.Ready {
		t.Errorf("reworked into %s", first.State)
	}
	_, err = d.Client.Rework(t.Context(), "wo", api.ReworkRequest{Reason: "again"})
	wantRefused(t, err, http.StatusConflict, api.CodeIllegalMove)
}

func TestAcceptanceTakesAMembersSession(t *testing.T) {
	d := apitest.Start(t)
	d.Completed(t, "wo", laneEnd())
	bare := api.NewUnixClient(d.Socket).Project(apitest.ProjectName)
	defer bare.Close()
	_, err := bare.Accept(t.Context(), "wo")
	wantRefused(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
	_, err = bare.Rework(t.Context(), "wo", api.ReworkRequest{Reason: "not mine to say"})
	wantRefused(t, err, http.StatusUnauthorized, api.CodeSessionRequired)
	if _, err := bare.Acceptance(t.Context(), "wo"); err != nil {
		t.Errorf("reading a standing takes no session: %v", err)
	}
	row, err := d.Client.WorkOrder(t.Context(), "wo")
	if err != nil {
		t.Fatal(err)
	}
	if row.State != workorders.Acceptance {
		t.Errorf("the work moved to %s", row.State)
	}
}
