package server_test

import (
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/test/apitest"
)

func decisionRecord(status, body string) []byte {
	return []byte("+++\nkind = \"decision\"\nid = \"adr-0007\"\ntitle = \"Retry budget\"\nstatus = \"" + status + "\"\nplan = \"retry\"\n+++\n\n" + body + "\n")
}

// decisionWorld records a decision record that is proposed and then accepted, beside the plan's spec.
func decisionWorld(t *testing.T) (*apitest.Daemon, *fake.WorkSource) {
	t.Helper()
	d, w, _ := documentsWorld(t)
	source := retrySource("The first draft.")
	source.Write("docs/decisions/0007-retry-budget.md", decisionRecord("proposed", "Every call has a budget."))
	w.Planned(source)
	source.Write("docs/decisions/0007-retry-budget.md", decisionRecord("accepted", "Every call has a budget."))
	w.Planned(source)
	return d, source
}

func TestTheDocumentsViewSaysTheStatusOfEachVersionOfADecisionRecord(t *testing.T) {
	t.Parallel()
	d, _ := decisionWorld(t)
	plan := planDocuments(t, d, "Retry budget")
	decision := recorded(t, plan, "decision:adr-0007")
	if decision.Kind != planning.FileDecision || decision.Title != "Retry budget" || decision.Path != "docs/decisions/0007-retry-budget.md" || len(decision.Versions) != 2 {
		t.Fatalf("decision %+v", decision)
	}
	if newest, first := decision.Versions[0], decision.Versions[1]; newest.Status != string(planning.DecisionAccepted) || first.Status != string(planning.DecisionProposed) {
		t.Errorf("statuses %q and %q", newest.Status, first.Status)
	}
	for _, version := range recorded(t, plan, "spec:api").Versions {
		if version.Status != "" {
			t.Errorf("a spec's version %d says status %q", version.Version, version.Status)
		}
	}
}

func TestARecordedDecisionVersionSaysItsStatus(t *testing.T) {
	t.Parallel()
	d, _ := decisionWorld(t)
	for version, want := range map[int64]string{0: "accepted", 1: "proposed", 2: "accepted"} {
		read, err := d.Client.RecordedDocument(t.Context(), "decision:adr-0007", version)
		if err != nil {
			t.Fatal(err)
		}
		if read.Version.Status != want || read.Body != "Every call has a budget.\n" {
			t.Errorf("version %d reads %+v", version, read)
		}
	}
}

// TestGoldenDecisionDocuments writes what the Documents view and the reader show of a decision record, which
// the web tests read.
func TestGoldenDecisionDocuments(t *testing.T) {
	t.Parallel()
	d, _ := decisionWorld(t)
	holdsGolden(t, "documents-decision", steady(pretty(t, readView[api.Documents](t, d, api.ViewDocuments, ""))))
	read, err := d.Client.RecordedDocument(t.Context(), "decision:adr-0007", 1)
	if err != nil {
		t.Fatal(err)
	}
	holdsGolden(t, "recorded-decision", steady(pretty(t, read)))
}
