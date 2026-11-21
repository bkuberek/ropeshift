package control

import (
	"encoding/json"
	"errors"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// errAssessed is what the stub's evidence answers when acceptance asks for the attempt's checks, which it does only once
// it has taken the request as raised for the commit the branch holds.
var errAssessed = errors.New("acceptance read the attempt's checks")

type namedPlanOrders struct {
	ports.WorkOrderWriter
	order workorders.WorkOrder
}

func (o namedPlanOrders) Get(string) (workorders.WorkOrder, error) { return o.order, nil }

type namedPlanEvidence struct {
	ports.EvidenceWriter
	artifacts []ledger.Artifact
}

func (e namedPlanEvidence) Artifacts(string) ([]ledger.Artifact, error) { return e.artifacts, nil }

func (e namedPlanEvidence) Attempt(string, int64) ([]workorders.Evidence, error) {
	return nil, errAssessed
}

type namedPlanTx struct {
	ports.WriteTx
	orders   namedPlanOrders
	evidence namedPlanEvidence
}

func (t namedPlanTx) WorkOrders() ports.WorkOrderWriter { return t.orders }
func (t namedPlanTx) Evidence() ports.EvidenceWriter    { return t.evidence }

// Planning is read only for the contract a work order pins, and the stub's work order pins none.
func (t namedPlanTx) Planning() ports.PlanningWriter { return nil }

// A request names the commit it was raised for as the commit itself, or as the file it read there, "path@commit".
// Approving it accepts the planning work that waits for acceptance at that commit, whichever way the request names it,
// and refuses a request raised for another commit.
func TestApprovingARequestAcceptsTheWorkWaitingAtTheCommitItNames(t *testing.T) {
	const held = "c2"
	for name, tt := range map[string]struct {
		source  string
		current bool
	}{
		"the commit":                    {source: held, current: true},
		"a file read at the commit":     {source: "plan.md@" + held, current: true},
		"a file read at another commit": {source: "plan.md@c1", current: false},
		"another commit":                {source: "c1", current: false},
	} {
		t.Run(name, func(t *testing.T) {
			detail, err := json.Marshal(planning.PlanDetail{Source: tt.source})
			if err != nil {
				t.Fatal(err)
			}
			approval := authority.Approval{ID: "a1", Kind: authority.KindPlan, WorkOrder: "wo-1", Plan: "p", PlanVersion: 2, Detail: string(detail)}
			tx := namedPlanTx{
				orders: namedPlanOrders{order: workorders.WorkOrder{ID: "wo-1", Kind: workorders.KindPlan, State: workorders.Acceptance, Fence: 2}},
				evidence: namedPlanEvidence{artifacts: []ledger.Artifact{
					{Kind: ledger.ArtifactBranch, Fence: 1, Digest: "c1"},
					{Kind: ledger.ArtifactBranch, Fence: 2, Digest: held},
				}},
			}
			err = acceptNamedPlan(tx, approval, ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"})
			if tt.current && !errors.Is(err, errAssessed) {
				t.Errorf("a request raised for the commit the branch holds answered %v, want it taken up for acceptance", err)
			}
			if !tt.current && !errors.Is(err, authority.ErrStaleDigest) {
				t.Errorf("a request raised for another commit answered %v, want %v", err, authority.ErrStaleDigest)
			}
		})
	}
}
