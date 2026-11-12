package workorders

import (
	"slices"
	"strings"
	"testing"
)

func passing() ReadyFacts {
	return ReadyFacts{
		Criteria:        1,
		ContractVersion: 4,
		ContractDigest:  "gitblob:abc",
		Dependencies:    []DependencyFact{{Known: true, Blocking: true, State: Done}},
	}
}

func TestDefinitionOfReady(t *testing.T) {
	tests := []struct {
		name   string
		change func(*ReadyFacts)
		want   []ReadyCheck
	}{
		{"everything passes", func(*ReadyFacts) {}, nil},
		{"no work order needs a dependency", func(f *ReadyFacts) { f.Dependencies = nil }, nil},
		{"a contract with no criterion", func(f *ReadyFacts) { f.Criteria = 0 }, []ReadyCheck{CheckCriteria}},
		{
			"a criterion that serves no requirement of the plan",
			func(f *ReadyFacts) { f.Untraced = []string{"criterion c1 maps to no requirement"} },
			[]ReadyCheck{CheckRequirements},
		},
		{
			"a dependency the ledger does not know",
			func(f *ReadyFacts) { f.Dependencies = []DependencyFact{{Blocking: true}} },
			[]ReadyCheck{CheckDependencies},
		},
		{
			"a non-blocking dependency must still be known",
			func(f *ReadyFacts) { f.Dependencies = []DependencyFact{{}} },
			[]ReadyCheck{CheckDependencies},
		},
		{
			"a blocking dependency that is not done",
			func(f *ReadyFacts) { f.Dependencies = []DependencyFact{{Known: true, Blocking: true, State: Running}} },
			[]ReadyCheck{CheckDependencies},
		},
		{
			"a non-blocking dependency that is not done",
			func(f *ReadyFacts) { f.Dependencies = []DependencyFact{{Known: true, State: Running}} },
			nil,
		},
		{"no version pinned", func(f *ReadyFacts) { f.ContractVersion = 0 }, []ReadyCheck{CheckContractVersion}},
		{"a pin with no digest", func(f *ReadyFacts) { f.ContractDigest = "" }, []ReadyCheck{CheckContractVersion}},
		{"an open blocking question", func(f *ReadyFacts) { f.OpenBlockingQuestions = 1 }, []ReadyCheck{CheckQuestions}},
		{"a repository the project does not hold", func(f *ReadyFacts) { f.UnknownRepository = "api" }, []ReadyCheck{CheckRepository}},
		{"an agent the roster cannot give", func(f *ReadyFacts) { f.AgentProblem = `unknown agent: the resolved roster holds no agent "ghost"` }, []ReadyCheck{CheckAgent}},
		{
			"every failure is named",
			func(f *ReadyFacts) {
				*f = ReadyFacts{OpenBlockingQuestions: 2, Dependencies: []DependencyFact{{Blocking: true}}, Untraced: []string{"criterion c1 maps to no requirement"}}
			},
			[]ReadyCheck{CheckCriteria, CheckRequirements, CheckDependencies, CheckContractVersion, CheckQuestions},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			facts := passing()
			tt.change(&facts)
			var got []ReadyCheck
			for _, failure := range DefinitionOfReady(facts) {
				if failure.Detail == "" {
					t.Errorf("%s has no detail", failure.Check)
				}
				got = append(got, failure.Check)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("failed %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTheRequirementsFailureNamesEveryCriterion(t *testing.T) {
	facts := passing()
	facts.Untraced = []string{"criterion c1 maps to no requirement", "criterion c2 names requirement PAY-99, which the plan does not have"}
	failed := DefinitionOfReady(facts)
	if len(failed) != 1 || failed[0].Check != CheckRequirements {
		t.Fatalf("failed %+v", failed)
	}
	for _, name := range []string{"c1", "c2", "PAY-99"} {
		if !strings.Contains(failed[0].Detail, name) {
			t.Errorf("the failure %q does not name %q", failed[0].Detail, name)
		}
	}
}
