package workorders

import (
	"fmt"
	"strings"
)

// ReadyCheck names one row of the definition of ready.
type ReadyCheck string

// The checks (FR-PLAN-5, FR-DOC-5).
const (
	CheckCriteria ReadyCheck = "acceptance_criteria"
	// CheckRequirements fails while a criterion of the pinned contract serves no requirement of its plan.
	CheckRequirements    ReadyCheck = "requirements"
	CheckDependencies    ReadyCheck = "dependencies"
	CheckContractVersion ReadyCheck = "contract_version"
	CheckQuestions       ReadyCheck = "questions"
)

// DependencyFact is what the definition of ready knows about one dependency.
type DependencyFact struct {
	Name     string
	Known    bool
	Blocking bool
	State    State
}

// ReadyFacts is everything the definition of ready reads. The function holds no
// other input, so the same ledger always gives the same answer.
type ReadyFacts struct {
	Criteria int
	// Untraced names each criterion of the pinned contract that serves no requirement its plan has,
	// and is empty for a plan with no requirements document.
	Untraced              []string
	ContractVersion       int64
	ContractDigest        string
	Dependencies          []DependencyFact
	OpenBlockingQuestions int
}

// ReadyFailure is one check that did not pass.
type ReadyFailure struct {
	Check  ReadyCheck `json:"check"`
	Detail string     `json:"detail"`
}

// DefinitionOfReady runs at every transition into `ready`, and returns every check
// that failed. An empty result means ready.
func DefinitionOfReady(f ReadyFacts) []ReadyFailure {
	var failed []ReadyFailure
	if f.Criteria == 0 {
		failed = append(failed, ReadyFailure{CheckCriteria, "the pinned contract has no acceptance criterion"})
	}
	if len(f.Untraced) > 0 {
		failed = append(failed, ReadyFailure{CheckRequirements, strings.Join(f.Untraced, "; ")})
	}
	if detail := dependencyProblem(f.Dependencies); detail != "" {
		failed = append(failed, ReadyFailure{CheckDependencies, detail})
	}
	if f.ContractVersion == 0 || f.ContractDigest == "" {
		failed = append(failed, ReadyFailure{CheckContractVersion, "no contract version is pinned with its digest"})
	}
	if f.OpenBlockingQuestions > 0 {
		failed = append(failed, ReadyFailure{CheckQuestions, fmt.Sprintf("%d blocking questions are open", f.OpenBlockingQuestions)})
	}
	return failed
}

// dependencyProblem names the first dependency that holds a work order back.
func dependencyProblem(dependencies []DependencyFact) string {
	for _, d := range dependencies {
		if !d.Known {
			return fmt.Sprintf("dependency %q is not known", d.Name)
		}
	}
	for _, d := range dependencies {
		if d.Blocking && d.State != Done {
			return fmt.Sprintf("dependency %q is %s", d.Name, d.State)
		}
	}
	return ""
}
