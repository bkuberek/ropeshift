package lanes_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/lanes"
)

func golden(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s:\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func sealed(t *testing.T, doc lanes.Handoff) (string, string) {
	t.Helper()
	body, digest, err := doc.Seal()
	if err != nil {
		t.Fatal(err)
	}
	return string(body), digest
}

func facts() lanes.HandoffFacts {
	return lanes.HandoffFacts{
		WorkOrder: "01JC8RV4KT", Contract: &lanes.ContractRef{ID: "01JC6M", Version: 4},
		ContextPack: &lanes.PackRef{ID: "01JC6N", Version: 2}, Branch: "lane/checkout-retry", Head: "4be81fa",
		SnapshotRef: "refs/ropeshift/checkpoints/01JCA2/3", DirtyPaths: []string{"internal/checkout/retry.go"},
		UnpushedCommits: 2, OpenQuestions: []string{"01JC9Z"}, OpenAssumptions: []string{"01JCA0"},
		Checks: []lanes.CheckFact{{Name: "unit", Status: "failed"}}, ModelResolved: "example-model-id",
	}
}

func narrative() *lanes.Narrative {
	return &lanes.Narrative{
		Done: []string{"Added the retry budget"}, InProgress: []string{"Idempotency key on the second attempt"},
		NextSteps: []string{"Fix the failing unit test"}, Risks: []string{"The payment stub does not model timeouts"},
		CarriesNextStep: true,
	}
}

func document() lanes.Handoff {
	return lanes.Handoff{
		Schema: lanes.HandoffSchema, Lane: "01JCA2", Generation: 3, Reason: lanes.HandoffContextThreshold,
		Deterministic: facts(), Narrative: narrative(),
	}
}

func TestAHandoffDocumentHasOneShape(t *testing.T) {
	body, digest := sealed(t, document())
	golden(t, "handoff-full.golden", body+"\n")
	if !strings.HasPrefix(digest, "sha256:") {
		t.Errorf("digest %q", digest)
	}
}

func TestADegradedHandoffHoldsOnlyWhatTheWorkerKnows(t *testing.T) {
	doc := document()
	doc.Narrative = nil
	body, _ := sealed(t, doc)
	golden(t, "handoff-degraded.golden", body+"\n")
	if !doc.Degraded() {
		t.Error("a document with no narrative must read as degraded")
	}
}

func TestAFactNobodyReadStaysOutOfTheDocument(t *testing.T) {
	doc := document()
	doc.Deterministic.OpenQuestions, doc.Deterministic.OpenAssumptions, doc.Deterministic.Checks = nil, nil, nil
	body, _ := sealed(t, doc)
	if strings.Contains(body, "open_questions") || strings.Contains(body, "checks") {
		t.Errorf("an unread list reads as an empty one: %s", body)
	}
	doc.Deterministic.OpenQuestions = []string{}
	body, _ = sealed(t, doc)
	if !strings.Contains(body, `"open_questions":[]`) {
		t.Errorf("a list read as empty must say so: %s", body)
	}
}

func TestSealingTwiceGivesOneDigest(t *testing.T) {
	_, first := sealed(t, document())
	_, second := sealed(t, document())
	if first != second {
		t.Errorf("%s and %s", first, second)
	}
	changed := document()
	changed.Narrative.Risks = []string{"another"}
	if _, third := sealed(t, changed); third == first {
		t.Error("a change kept the digest")
	}
}

func TestADocumentReadsBack(t *testing.T) {
	body, _ := sealed(t, document())
	read, err := lanes.ReadHandoff([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	again, _ := sealed(t, read)
	if again != body {
		t.Errorf("read back as %s", again)
	}
}

func TestAnInvalidDocumentIsRefused(t *testing.T) {
	tests := []struct {
		name   string
		change func(*lanes.Handoff)
	}{
		{"no schema", func(d *lanes.Handoff) { d.Schema = "" }},
		{"a schema this build cannot read", func(d *lanes.Handoff) { d.Schema = "ropeshift.handoff/v2" }},
		{"no lane", func(d *lanes.Handoff) { d.Lane = "" }},
		{"no generation", func(d *lanes.Handoff) { d.Generation = 0 }},
		{"no reason", func(d *lanes.Handoff) { d.Reason = "" }},
		{"no work order", func(d *lanes.Handoff) { d.Deterministic.WorkOrder = "" }},
		{"a narrative that says nothing", func(d *lanes.Handoff) { d.Narrative = &lanes.Narrative{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := document()
			tt.change(&doc)
			if _, _, err := doc.Seal(); !errors.Is(err, lanes.ErrBadHandoff) {
				t.Errorf("sealed: %v", err)
			}
		})
	}
}

func TestANarrativeIsReadOrRefused(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"what the design prints", `{"done":["a"],"in_progress":["b"],"next_steps":["c"],"risks":["d"],"carries_next_step":true}`, true},
		{"no statement about the next step", `{"done":["a"],"next_steps":["c"]}`, true},
		{"not JSON", `the work is half done`, false},
		{"not an object", `["a"]`, false},
		{"a list that is a string", `{"done":"a","next_steps":["c"]}`, false},
		{"a key the schema does not name", `{"next_steps":["c"],"mood":"tired"}`, false},
		{"nothing said", `{"done":[],"in_progress":[],"next_steps":[],"risks":[]}`, false},
		{"a blank item", `{"next_steps":["  "]}`, false},
		{"two documents", `{"next_steps":["c"]}{"next_steps":["d"]}`, false},
		{"too long", `{"next_steps":["` + strings.Repeat("x", lanes.MaxNarrativeBytes) + `"]}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := lanes.ParseNarrative([]byte(tt.raw))
			if tt.ok && err != nil {
				t.Errorf("refused: %v", err)
			}
			if !tt.ok && !errors.Is(err, lanes.ErrBadHandoff) {
				t.Errorf("read: %v", err)
			}
		})
	}
}

// An absent statement means the handoff does not carry the next step (D11).
func TestAnAbsentStatementReadsAsFalse(t *testing.T) {
	read, err := lanes.ParseNarrative([]byte(`{"next_steps":["c"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if read.CarriesNextStep {
		t.Error("an absent statement read as true")
	}
	raw, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"carries_next_step":false`) {
		t.Errorf("the statement was not written: %s", raw)
	}
}

func TestTheInstructionNamesTheToolAndTheSchema(t *testing.T) {
	golden(t, "handoff-instruction.golden", lanes.HandoffInstruction())
}

func TestABriefCarriesTheHandoff(t *testing.T) {
	doc := document()
	brief := lanes.Brief(lanes.BriefSpec{WorkOrder: "wo", Title: "T", Statement: "S", Criteria: []string{"c1: It works"}, Handoff: &doc})
	golden(t, "brief-handoff.golden", brief)
}

func TestABriefStatesAMissingNarrative(t *testing.T) {
	doc := document()
	doc.Narrative = nil
	brief := lanes.Brief(lanes.BriefSpec{WorkOrder: "wo", Title: "T", Statement: "S", Criteria: []string{"c1: It works"}, Handoff: &doc})
	golden(t, "brief-degraded.golden", brief)
}

func TestABriefWithNoSnapshotSaysWhereTheWorkIs(t *testing.T) {
	doc := document()
	doc.Narrative, doc.Deterministic.SnapshotRef = nil, ""
	brief := lanes.Brief(lanes.BriefSpec{WorkOrder: "wo", Title: "T", Handoff: &doc})
	if !strings.Contains(brief, "4be81fa") || strings.Contains(brief, "refs/ropeshift") {
		t.Errorf("brief:\n%s", brief)
	}
}

func TestAnOpenHandoffNeedsFinishing(t *testing.T) {
	var none *lanes.HandoffProgress
	tests := []struct {
		name string
		in   *lanes.HandoffProgress
		want bool
	}{
		{"none", none, false},
		{"under way", &lanes.HandoffProgress{Reason: "r"}, true},
		{"stored and not yet begun", &lanes.HandoffProgress{Reason: "r", Document: []byte("{}")}, true},
		{"its generation began", &lanes.HandoffProgress{Reason: "r", Document: []byte("{}"), Begun: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Open(); got != tt.want {
				t.Errorf("Open: %v", got)
			}
		})
	}
}
