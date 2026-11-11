package planning

import (
	"reflect"
	"testing"
)

// adoptedPlan is a plan for an existing project with a record of every group: a met requirement and an open
// one, an existing milestone with an existing shared contract, a contract whose one work order continues a
// branch, a contract with fresh work beside a continued branch, a new milestone, and a branch left out.
func adoptedPlan() AdoptedFiles {
	return AdoptedFiles{
		Plan: PlanFile{ID: "payments-v2", LeftOut: []LeftOut{{Ref: "branch:spike/old-checkout", Reason: "An abandoned experiment"}}},
		Requirements: []RequirementsFile{{ID: "payments", Requirements: []RequirementSpec{
			{ID: "PAY-12", Text: "It retries"},
			{ID: "PAY-11", Text: "One client", Status: RequirementMet, Evidence: []string{"api:client.go", "api:client_test.go"}},
		}}},
		Milestones: []MilestoneFile{
			{ID: "m1-reliable", Title: "Reliable checkout", Priority: 1},
			{ID: "m0-today", Title: "Payments as they are", Existing: true},
		},
		Contracts: []ContractFile{
			{ID: "retry", Title: "Retry failed calls", Milestone: "m1-reliable", WorkOrders: []WorkOrderSpec{
				{ID: "retry-finish", Title: "Finish the retry branch", Repo: "api", Continues: "feature/checkout-retry"},
			}},
			{ID: "receipts", Title: "Email receipts", Milestone: "m1-reliable", WorkOrders: []WorkOrderSpec{
				{ID: "receipts-send", Title: "Send them"},
				{ID: "receipts-finish", Title: "Finish the template branch", Continues: "wip/receipt-template"},
			}},
			{ID: "client", Title: "Payments client", Milestone: "m0-today", Shared: true, Existing: true,
				Criteria: []Criterion{{ID: "c1", Text: "One client", Requirements: []string{"PAY-11"}}}},
		},
	}
}

// ADR-0023: the approval of an adopted plan lists every claim with its evidence, each continued branch, what
// is new, and what is left out with its reason.
func TestAnAdoptedPlanIsGroupedAsItsApprovalShowsIt(t *testing.T) {
	got := GroupAdoption(adoptedPlan())
	want := Adoption{
		Exists: []ExistingRecord{
			{Ref: "requirement:PAY-11", Title: "One client", Evidence: []string{"api:client.go", "api:client_test.go"}},
			{Ref: "milestone:m0-today", Title: "Payments as they are", Cites: []string{"contract:client"}},
			{Ref: "contract:client", Title: "Payments client", Evidence: []string{"api:client.go", "api:client_test.go"}, Cites: []string{"requirement:PAY-11"}},
		},
		Continues: []ContinuedWork{
			{Contract: "contract:receipts", WorkOrder: "receipts-finish", Title: "Finish the template branch", Branch: "wip/receipt-template"},
			{Contract: "contract:retry", WorkOrder: "retry-finish", Title: "Finish the retry branch", Repo: "api", Branch: "feature/checkout-retry"},
		},
		New: []NewRecord{
			{Ref: "milestone:m1-reliable", Title: "Reliable checkout"},
			{Ref: "contract:receipts", Title: "Email receipts", WorkOrders: []string{"receipts-send"}},
		},
		LeftOut: []LeftOut{{Ref: "branch:spike/old-checkout", Reason: "An abandoned experiment"}},
		Changes: []FileChange{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the plan is grouped as\n%+v\nwant\n%+v", got, want)
	}
}

// A plan that holds none of a group has an empty group, so a reader lists it as empty and not as missing.
func TestAPlanWithNothingToGroupHasEmptyGroups(t *testing.T) {
	got := GroupAdoption(AdoptedFiles{Plan: PlanFile{ID: "empty"}})
	if got.Exists == nil || got.Continues == nil || got.New == nil || got.LeftOut == nil || got.Changes == nil {
		t.Errorf("empty groups read as missing: %+v", got)
	}
}
