package server_test

import (
	"math"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/test/apitest"
)

func spendKey(view api.SpendPolicyView, key string) string {
	for _, k := range view.Keys {
		if k.Key == key {
			return k.Value
		}
	}
	return "<no such key>"
}

// A member reads each key of the spend policies with its value and version, and the spend of each open work order
// against its limits, labeled as an estimate at list price (FR-SPEND-18).
func TestMemberReadsTheSpendPolicyAndEachWorkOrdersSpend(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	if _, err := d.client.CreateWorkOrder(ctx, d.workIn("wo-spend", "Add the retry budget")); err != nil {
		t.Fatal(err)
	}
	view, err := d.client.Spend(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if view.Project.Version != 1 || view.Installation.Version != 1 || len(view.Project.Keys) != len(authority.SpendPolicyKeys) ||
		len(view.Installation.Keys) != len(authority.InstallationSpendPolicyKeys) {
		t.Errorf("a project on the built-in policies reads %+v", view)
	}
	if spendKey(view.Project, "hard_multiplier") != "2" || spendKey(view.Project, "no_history_limit.medium") != "25" ||
		spendKey(view.Installation, "orchestrator_class") != "standard" {
		t.Errorf("the keys read %+v and %+v", view.Project.Keys, view.Installation.Keys)
	}
	var seen *ledger.WorkOrderSpend
	for i := range view.WorkOrders {
		if view.WorkOrders[i].WorkOrder == "wo-spend" {
			seen = &view.WorkOrders[i]
		}
	}
	if seen == nil || math.Abs(seen.HardLimit-25) > 1e-9 || seen.Charged.ChargedUSD != 0 {
		t.Fatalf("the open work order reads %+v among %+v", seen, view.WorkOrders)
	}
	policy, err := authority.DefaultSpendPolicy().Set("no_history_limit.medium", "40")
	if err != nil {
		t.Fatal(err)
	}
	policy.Version = 2
	if _, err := d.client.PublishPolicy(ctx, policy.Body()); err != nil {
		t.Fatal(err)
	}
	if view, err = d.client.Spend(ctx); err != nil || view.Project.Version != 2 || spendKey(view.Project, "no_history_limit.medium") != "40" || view.Project.AuthoredBy == "" {
		t.Errorf("after version 2 the project reads %+v: %v", view.Project, err)
	}
	one, err := d.client.WorkOrderSpend(ctx, "wo-spend")
	if err != nil || math.Abs(one.HardLimit-40) > 1e-9 || one.PolicyVersion != 2 || !strings.HasPrefix(one.Label, "estimated, at list price") {
		t.Errorf("the work order's spend reads %+v: %v", one, err)
	}
	_, err = d.client.WorkOrderSpend(ctx, "no-such-work-order")
	refusedWith(t, err, http.StatusNotFound, api.CodeNotFound)
}

// The spend of a work order is a member's read: a lane records usage to the ledger through its worker, and the
// control plane folds it, so charged spend follows what the lanes reported (FR-SPEND-3).
func TestAWorkOrdersChargedSpendFollowsWhatItsLanesReported(t *testing.T) {
	d := startTalk(t)
	ctx := t.Context()
	claimWork(t, d, "wo-fold", "claim-fold")
	err := d.Store.Write(ctx, apitest.Scope, "spend.fixture", func(tx ports.WriteTx) error {
		if err := tx.Lanes().Start("lane-fold", "wo-fold", 1, ledger.LanePolicies{}); err != nil {
			return err
		}
		if err := tx.Lanes().StartGeneration(ledger.Generation{Lane: "lane-fold", Number: 1, StartReason: ledger.StartInitial}); err != nil {
			return err
		}
		if _, err := tx.Lanes().ReportUsage("lane-fold", 1, ledger.GenerationUsage{CostUSD: 0.4, KnownTurns: 4, UnknownTurns: 2}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "note.added", Actor: talkMember, Subject: ledger.Subject{Kind: "note", ID: "fold"}, Data: []byte(`{}`)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	spend, err := d.client.WorkOrderSpend(ctx, "wo-fold")
	if err != nil || math.Abs(spend.Charged.ChargedUSD-0.6) > 1e-9 || spend.Charged.EstimatedTurns != 2 ||
		spend.Label != "estimated, at list price, with 2 turns estimated by average" {
		t.Errorf("the work order is charged %+v, labeled %q: %v", spend.Charged, spend.Label, err)
	}
}
