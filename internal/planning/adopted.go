package planning

import (
	"cmp"
	"slices"
	"strings"
)

// The ways a branch changes a file, as a plan approval lists them.
const (
	ChangeAdded   = "added"
	ChangeChanged = "changed"
	ChangeMoved   = "moved"
	ChangeDeleted = "deleted"
)

// FileChange is one file a branch changed since it began: added, changed, deleted, or moved from From to
// Path, which git follows as a rename.
type FileChange struct {
	Status string `json:"status"`
	Path   string `json:"path"`
	From   string `json:"from,omitempty"`
}

// ExistingRecord is a claim a plan for an existing project makes: a requirement the code meets, or a
// milestone or a contract it delivers. Evidence is what shows it, as `<repository>:<path>`, and Cites the
// records the claim rests on: the requirements an existing contract's criteria serve, and the existing
// contracts of an existing milestone.
type ExistingRecord struct {
	Ref      string   `json:"ref"`
	Title    string   `json:"title"`
	Evidence []string `json:"evidence,omitempty"`
	Cites    []string `json:"cites,omitempty"`
}

// ContinuedWork is a work order that finishes an in-flight branch.
type ContinuedWork struct {
	Contract  string `json:"contract"`
	WorkOrder string `json:"work_order"`
	Title     string `json:"title"`
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch"`
}

// NewRecord is a milestone or a contract the plan adds, and for a contract the work orders that start
// fresh. A contract whose every work order continues a branch is what continues, not what is new.
type NewRecord struct {
	Ref        string   `json:"ref"`
	Title      string   `json:"title"`
	WorkOrders []string `json:"work_orders,omitempty"`
}

// Adoption is a plan for an existing project as its approval shows it (ADR-0023): what the code already
// does, with the evidence the person stands behind by approving; the in-flight branches it finishes; what
// it adds; what it leaves out and why; and every file the adoption branch added, changed, or moved.
// ChangesUnread says why the branch's changes could not be read, when they could not.
type Adoption struct {
	Exists        []ExistingRecord `json:"exists"`
	Continues     []ContinuedWork  `json:"continues"`
	New           []NewRecord      `json:"new"`
	LeftOut       []LeftOut        `json:"left_out"`
	Changes       []FileChange     `json:"changes"`
	ChangesUnread string           `json:"changes_unread,omitempty"`
}

// AdoptedFiles are the files of one plan that the grouping reads.
type AdoptedFiles struct {
	Plan         PlanFile
	Requirements []RequirementsFile
	Milestones   []MilestoneFile
	Contracts    []ContractFile
}

// GroupAdoption sorts a plan's records into what exists, what continues, what is new, and what is left out.
// Each group is in the order of its refs, so the same files give the same grouping.
func GroupAdoption(files AdoptedFiles) Adoption {
	out := Adoption{Exists: []ExistingRecord{}, Continues: []ContinuedWork{}, New: []NewRecord{}, Changes: []FileChange{}}
	out.LeftOut = append([]LeftOut{}, files.Plan.LeftOut...)
	met := map[string]RequirementSpec{}
	var requirements, milestones, contracts []ExistingRecord
	for _, file := range files.Requirements {
		for _, requirement := range file.Requirements {
			if requirement.Met() {
				met[requirement.ID] = requirement
				requirements = append(requirements, ExistingRecord{
					Ref: "requirement:" + requirement.ID, Title: requirement.Text, Evidence: slices.Clone(requirement.Evidence),
				})
			}
		}
	}
	held := map[string][]string{}
	for _, contract := range files.Contracts {
		if !contract.Existing {
			out.addWork(contract)
			continue
		}
		record := ExistingRecord{Ref: ContractRef(contract.ID), Title: contract.Title}
		for _, key := range contractRequirements(contract) {
			record.Cites = append(record.Cites, "requirement:"+key)
			for _, cited := range met[key].Evidence {
				if !slices.Contains(record.Evidence, cited) {
					record.Evidence = append(record.Evidence, cited)
				}
			}
		}
		contracts = append(contracts, record)
		if contract.Milestone != "" {
			held[contract.Milestone] = append(held[contract.Milestone], ContractRef(contract.ID))
		}
	}
	for _, milestone := range files.Milestones {
		ref := DocumentRef(FileMilestone, milestone.ID)
		if !milestone.Existing {
			out.New = append(out.New, NewRecord{Ref: ref, Title: milestone.Title})
			continue
		}
		cites := slices.Clone(held[milestone.ID])
		slices.Sort(cites)
		milestones = append(milestones, ExistingRecord{Ref: ref, Title: milestone.Title, Cites: cites})
	}
	for _, group := range [][]ExistingRecord{requirements, milestones, contracts} {
		slices.SortStableFunc(group, func(a, b ExistingRecord) int { return strings.Compare(a.Ref, b.Ref) })
		out.Exists = append(out.Exists, group...)
	}
	slices.SortStableFunc(out.Continues, func(a, b ContinuedWork) int {
		return cmp.Or(strings.Compare(a.Contract, b.Contract), strings.Compare(a.WorkOrder, b.WorkOrder))
	})
	slices.SortStableFunc(out.New, func(a, b NewRecord) int {
		// Milestones come before contracts, as a person reads a plan.
		return cmp.Or(strings.Compare(EntryKind(b.Ref), EntryKind(a.Ref)), strings.Compare(a.Ref, b.Ref))
	})
	return out
}

// addWork puts a contract the plan adds where it belongs: each work order that continues a branch with
// what continues, and the contract with what is new unless every work order it has continues one.
func (a *Adoption) addWork(contract ContractFile) {
	record := NewRecord{Ref: ContractRef(contract.ID), Title: contract.Title}
	continued := 0
	for _, order := range contract.WorkOrders {
		if order.Continues == "" {
			record.WorkOrders = append(record.WorkOrders, order.ID)
			continue
		}
		continued++
		a.Continues = append(a.Continues, ContinuedWork{
			Contract: ContractRef(contract.ID), WorkOrder: order.ID, Title: order.Title, Repo: order.Repo, Branch: order.Continues,
		})
	}
	if continued == 0 || continued < len(contract.WorkOrders) {
		a.New = append(a.New, record)
	}
}

// contractRequirements are the requirement keys a contract's criteria and its own front matter name, each
// once, in the order they first appear.
func contractRequirements(contract ContractFile) []string {
	keys := slices.Clone(contract.Requirements)
	for _, criterion := range contract.Criteria {
		for _, key := range criterion.Requirements {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	return keys
}
