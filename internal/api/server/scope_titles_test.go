package server_test

import (
	"maps"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/apitest"
)

// titledScopes records a decision under each kind of scope, a decision whose repository's name is its identity,
// and one with a scope written before the grammar, which the store keeps as it was. It says the title each
// decision's scope reads with, by the decision's ID.
func titledScopes(t *testing.T) (*apitest.Daemon, map[string]string) {
	t.Helper()
	d := apitest.Start(t)
	w := apitest.NewWorld(t, d)
	w.Recorded("wo-trim", "Trim the log")
	w.ChangeContract("wo-trim", func(next *planning.Contract) { next.Title = "Trim the log to a day" })
	d.HoldRepository(t, "checkout")
	var contract string
	if err := d.Store.Read(t.Context(), apitest.Scope, func(tx ports.ReadTx) error {
		order, err := tx.WorkOrders().Get("wo-trim")
		contract = order.ContractID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	scopes := map[string]string{
		"d-plan": "plan:" + w.PlanOf("wo-trim"), "d-contract": "contract:" + contract, "d-work": "wo:wo-trim", "d-repository": "repo:checkout",
	}
	for id, scope := range scopes {
		if _, err := d.Client.RecordDecision(t.Context(), api.RecordDecision{
			ID: id, Status: questions.DecisionDecided, Statement: "Decided under " + scope, Scope: scope, Reversibility: questions.Reversible,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Store.Write(t.Context(), apitest.Scope, "test.decision", func(tx ports.WriteTx) error {
		if err := tx.Questions().InsertDecision(questions.Decision{
			ID: "d-legacy", Status: questions.DecisionDecided, Statement: "Decided before the grammar", Scope: "payments/retries",
		}); err != nil {
			return err
		}
		_, err := tx.Append(ledger.EventDraft{Type: "test.decision", Actor: ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}, Subject: ledger.Subject{Kind: "decision", ID: "d-legacy"}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return d, map[string]string{
		"d-plan": "Trim the log", "d-contract": "Trim the log to a day", "d-work": "Trim the log", "d-repository": "", "d-legacy": "",
	}
}

// TestADecisionReadsWithTheTitleOfTheRecordItsScopeNames holds that every read of decisions names the record
// each scope names, in the same answer: the list, one decision, and the decisions view.
func TestADecisionReadsWithTheTitleOfTheRecordItsScopeNames(t *testing.T) {
	t.Parallel()
	d, want := titledScopes(t)
	listed := map[string]string{}
	rows, err := d.Client.Decisions(t.Context(), api.RecordFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		listed[row.ID] = row.ScopeTitle
	}
	if !maps.Equal(listed, want) {
		t.Errorf("the list titles the scopes %v, want %v", listed, want)
	}
	for id, title := range want {
		read, err := d.Client.Decision(t.Context(), id)
		if err != nil || read.ScopeTitle != title {
			t.Errorf("%s reads with the scope title %q, want %q: %v", id, read.ScopeTitle, title, err)
		}
	}
	viewed := map[string]string{}
	for _, line := range readView[api.Decisions](t, d, api.ViewDecisions, "").Decisions {
		viewed[line.ID] = line.ScopeTitle
	}
	if !maps.Equal(viewed, want) {
		t.Errorf("the decisions view titles the scopes %v, want %v", viewed, want)
	}
}
