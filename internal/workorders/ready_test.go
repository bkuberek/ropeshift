package workorders

import (
	"slices"
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
		{
			"every failure is named",
			func(f *ReadyFacts) {
				*f = ReadyFacts{OpenBlockingQuestions: 2, Dependencies: []DependencyFact{{Blocking: true}}}
			},
			[]ReadyCheck{CheckCriteria, CheckDependencies, CheckContractVersion, CheckQuestions},
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
