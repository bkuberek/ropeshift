package control_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/templates"
)

// heldPlanFiles are the plan a new project starts from with its own title, so that only the goal is still the
// template's.
func heldPlanFiles(t *testing.T) map[string]string {
	t.Helper()
	files := map[string]string{}
	for path, name := range map[string]string{"plan/plan.md": "workspace/plan/plan.md", "plan/goal.md": "workspace/plan/goal.md", "plan/requirements.md": "workspace/plan/requirements.md"} {
		text, err := templates.Render(name, templates.Data{Project: "harbor"})
		if err != nil {
			t.Fatal(err)
		}
		files[path] = text
	}
	files["plan/plan.md"] = withProse(strings.ReplaceAll(files["plan/plan.md"], planning.PlaceholderPlanTitle, "Harbor berth booking"), "The API first, then the page.")
	return files
}

// withProse replaces the prose after a file's front matter.
func withProse(text, body string) string {
	at := strings.LastIndex(text, "\n+++\n")
	return text[:at+len("\n+++\n")] + "\n" + body + "\n"
}

// A request that holds a plan version back may name a work order the ledger no longer holds. Asking again about the
// version, once its goal is written, does not fail the intake that reads it.
func TestAHeldVersionWithAMissingWorkOrderIsStillOffered(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	store := r.stores["pa"]
	files := heldPlanFiles(t)
	source := fake.NewWorkSource()
	for path, text := range files {
		source.Write(path, []byte(text))
	}
	if report, err := control.NewIntake(store).Poll(ctx, "pa", source); err != nil || len(report.Problems) != 0 {
		t.Fatalf("intake of the held plan: %+v, %v", report, err)
	}
	held := openPlanRequests(t, r)
	if len(held) != 1 || len(planDetailLines(held[0])) == 0 {
		t.Fatalf("the plan with the template's goal holds %+v", held)
	}
	// The request names a work order that is gone.
	err := store.Write(ctx, "pa", "test.approval", func(tx ports.WriteTx) error {
		named := held[0]
		named.ID, named.WorkOrder = "raised-for-a-gone-order", "wo-gone"
		if _, err := tx.Approvals().Insert(named); err != nil {
			return err
		}
		closed := held[0]
		closed.State = authority.ApprovalDenied
		if _, err := tx.Approvals().Save(closed, authority.ApprovalPending); err != nil {
			return err
		}
		event, err := authority.ApprovalEvent(named, ledger.Actor{Kind: ledger.ActorSystem, ID: "intake"})
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	goal := strings.NewReplacer(planning.PlaceholderGoalTitle, "Boats book a berth online", planning.PlaceholderMeasure, "A boat books a berth without calling the office").Replace(files["plan/goal.md"])
	source.Write("plan/goal.md", []byte(withProse(goal, "Berths are booked by phone today.")))
	if report, err := control.NewIntake(store).Poll(ctx, "pa", source); err != nil || len(report.Problems) != 0 {
		t.Fatalf("intake once the goal is written: %+v, %v", report, err)
	}
	offered := openPlanRequests(t, r)
	if len(offered) != 1 || len(planDetailLines(offered[0])) != 0 || offered[0].PlanVersion != held[0].PlanVersion {
		t.Errorf("the version with its goal written is offered as %+v", offered)
	}
}

func openPlanRequests(t *testing.T, r *planeRig) []authority.Approval {
	t.Helper()
	var out []authority.Approval
	err := r.stores["pa"].Read(t.Context(), "pa", func(tx ports.ReadTx) error {
		rows, err := tx.Approvals().All()
		for _, row := range rows {
			if row.Kind == authority.KindPlan && row.Open() {
				out = append(out, row)
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// planDetailLines are what a request says its plan still lacks.
func planDetailLines(request authority.Approval) []string {
	var detail planning.PlanDetail
	if json.Unmarshal([]byte(request.Detail), &detail) != nil {
		return nil
	}
	return detail.Unfinished
}
