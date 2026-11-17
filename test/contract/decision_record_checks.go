package contract

import (
	"encoding/json"
	"slices"
	"strings"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/planning"
	"ropeshift.com/ropeshift/internal/ports"
)

// decisionOf renders a decision record. An empty plan or status leaves the key out.
func decisionOf(id, title, plan, status, body string) string {
	out := "+++\nkind = \"decision\"\nid = \"" + id + "\"\ntitle = \"" + title + "\"\n"
	if status != "" {
		out += "status = \"" + status + "\"\n"
	}
	if plan != "" {
		out += "plan = \"" + plan + "\"\n"
	}
	return out + "+++\n\n" + body + "\n"
}

// decisionStatusOf reads the status a version of a decision record holds.
func decisionStatusOf(version planning.DocumentVersion) (planning.DecisionStatus, error) {
	var file planning.DecisionFile
	if err := json.Unmarshal(version.Fields, &file); err != nil {
		return "", err
	}
	return file.Status, nil
}

func checkDecisionStatusVersions(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	const decisionFile = "docs/decisions/0007-retry-budget.md"
	source.Write(decisionFile, []byte(decisionOf("adr-0007", "Retry budget", "", "", "Every call has a budget.")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if len(report.Problems) != 0 || report.Records != 1 {
		return Violatedf("a decision record with no plan and no status: %+v", report)
	}
	documents, err := r.documentRecords(s)
	if err != nil {
		return err
	}
	if len(documents) != 1 || documents[0].ID != "decision:adr-0007" || documents[0].Kind != planning.FileDecision || documents[0].PlanID != "" {
		return Violatedf("documents %+v", documents)
	}
	// The same file again records nothing.
	if report, err = r.intake(s, source); err != nil || report.Records != 0 {
		return Violatedf("report %+v: %v", report, err)
	}
	source.Write(decisionFile, []byte(decisionOf("adr-0007", "Retry budget", "", "accepted", "Every call has a budget.")))
	if report, err = r.intake(s, source); err != nil || report.Records != 1 || len(report.Problems) != 0 {
		return Violatedf("a status change: %+v: %v", report, err)
	}
	source.Write(decisionFile, []byte(decisionOf("adr-0007", "Retry budget", "", "superseded", "Every call has a budget.")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	versions, err := r.documentVersionsOf(s, "decision:adr-0007")
	if err != nil {
		return err
	}
	want := []planning.DecisionStatus{planning.DecisionProposed, planning.DecisionAccepted, planning.DecisionSuperseded}
	if len(versions) != len(want) {
		return Violatedf("%d versions, want %d: %+v", len(versions), len(want), versions)
	}
	for i, version := range versions {
		status, err := decisionStatusOf(version)
		if err != nil {
			return err
		}
		if version.Version != int64(i+1) || status != want[i] || version.Body != "Every call has a budget.\n" {
			return Violatedf("version %d holds status %q and %+v, want status %q", i+1, status, version, want[i])
		}
	}
	// A file that states an unknown status is a problem, and records nothing.
	source.Write(decisionFile, []byte(decisionOf("adr-0007", "Retry budget", "", "approved", "Every call has a budget.")))
	if report, err = r.intake(s, source); err != nil {
		return err
	}
	if len(report.Problems) != 1 || report.Problems[0].Path != decisionFile || !strings.Contains(report.Problems[0].Detail, "approved") || report.Records != 0 {
		return Violatedf("an unknown status: %+v", report)
	}
	if err := r.CheckRevisions(s, r.Scope); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

func checkContractPinsDecision(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(planWith(documentsPlan, "Payments v2", []string{"decision:adr-0007"}, "Stripe")))
	source.Write("decision.md", []byte(decisionOf("adr-0007", "Retry budget", documentsPlan, "accepted", "Every call has a budget.")))
	source.Write(intakeFile, []byte(citing("alpha", documentsPlan, "One.", "decision:adr-0007")))
	report, err := r.intake(s, source)
	if err != nil {
		return err
	}
	if report.Versions != 1 || len(report.Problems) != 0 {
		return Violatedf("report %+v", report)
	}
	first, err := r.contractVersion(s, "alpha", 1)
	if err != nil {
		return err
	}
	want := []planning.DocumentPin{{Document: "decision:adr-0007", Version: 1}}
	if !slices.Equal(first.Documents, want) {
		return Violatedf("pins %+v, want %+v", first.Documents, want)
	}
	// The record is superseded. The contract did not change, so it keeps the version it read.
	source.Write("decision.md", []byte(decisionOf("adr-0007", "Retry budget", documentsPlan, "superseded", "Every call has a budget.")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	contracts, err := r.readContracts(s)
	if err != nil {
		return err
	}
	if len(contracts) != 1 || !slices.Equal(contracts[0].Documents, want) {
		return Violatedf("contracts %+v moved with the record", contracts)
	}
	// A new version of the contract reads the record as it stands then.
	source.Write(intakeFile, []byte(citing("alpha", documentsPlan, "Two.", "decision:adr-0007")))
	if _, err := r.intake(s, source); err != nil {
		return err
	}
	second, err := r.contractVersion(s, "alpha", 2)
	if err != nil {
		return err
	}
	if want = []planning.DocumentPin{{Document: "decision:adr-0007", Version: 2}}; !slices.Equal(second.Documents, want) {
		return Violatedf("version 2 pins %+v, want %+v", second.Documents, want)
	}
	if err := r.CheckRevisions(s, r.Scope); err != nil {
		return err
	}
	return r.CheckPlanning(s)
}

// checkPackHoldsCitedDecision reads a lane's pack for a contract that cites a decision record: the pack holds
// the version the contract read, with the status it had then, after the record has moved on.
func checkPackHoldsCitedDecision(r *Rig) error {
	s := r.Store()
	source := fake.NewWorkSource()
	source.Write("plan.md", []byte(PlanText(documentsPlan, "Payments v2", "")))
	source.Write("decision.md", []byte(decisionOf("adr-0007", "Retry budget", documentsPlan, "accepted", "Every call has a budget.")))
	source.Write(intakeFile, []byte(citing("alpha", documentsPlan, "One.", "decision:adr-0007")))
	status, err := newStatusRigOn(r, s, func(store ports.Store) (string, error) {
		if _, err := r.intake(store, source); err != nil {
			return "", err
		}
		order, err := r.orderByTitle(store, "The work order of alpha")
		if err != nil {
			return "", err
		}
		_, err = r.NewOps(store).Pass(r.Ctx, r.Scope, order.ID, r.member())
		return order.ID, err
	})
	if err != nil {
		return err
	}
	source.Write("decision.md", []byte(decisionOf("adr-0007", "Retry budget", documentsPlan, "superseded", "Every call has a budget.")))
	if _, err := r.intake(status.store, source); err != nil {
		return err
	}
	pack, err := status.newestPack()
	if err != nil {
		return err
	}
	if got := itemRefs(pack); !slices.Contains(got, "document decision:adr-0007@1") {
		return Violatedf("the pack holds %v", got)
	}
	for _, text := range []string{"## document decision:adr-0007@1: Retry budget", "Every call has a budget.", "Status:\n- accepted"} {
		if !strings.Contains(pack.Text, text) {
			return Violatedf("the pack does not say %q: %s", text, pack.Text)
		}
	}
	if strings.Contains(pack.Text, "superseded") {
		return Violatedf("the pack moved with the record: %s", pack.Text)
	}
	return r.CheckPacks(status.store)
}
