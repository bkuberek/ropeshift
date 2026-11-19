package ledger

const (
	// SubjectConsolidation is what the checks of a merged consolidation are about: the adopt work order whose branch
	// imported a design repository's history.
	SubjectConsolidation = "consolidation"
	// EventConsolidationChecked records whether the base branch of a merged consolidation contains the design
	// repository's tip it imported (FR-ADOPT-13).
	EventConsolidationChecked = "consolidation.checked"
)

// ConsolidationCheck is the data of EventConsolidationChecked: the branch that merged and the base it merged into,
// the commit the base stood at, the design tip the adoption imported, and whether the base contains it. Unread says
// why the check could not answer, when it could not.
type ConsolidationCheck struct {
	Branch     string `json:"branch"`
	Base       string `json:"base"`
	BaseCommit string `json:"base_commit,omitempty"`
	DesignTip  string `json:"design_tip,omitempty"`
	Contained  bool   `json:"contained"`
	Unread     string `json:"unread,omitempty"`
}

// Missing says the check found the base without the design tip.
func (c ConsolidationCheck) Missing() bool { return !c.Contained && c.Unread == "" }
