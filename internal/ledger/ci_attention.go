package ledger

// A member's word and CI's findings are events about the work order's pull request, beside the record each leaves.
const (
	// EventCIWaitExtended records Wait longer on a head: the time it added to the CI timeout.
	EventCIWaitExtended = "ci.wait_extended"
	// EventCIRerunRequested records a rerun of a head's checks, a member's or an automatic one, and who asked.
	EventCIRerunRequested = "ci.rerun_requested"
	// EventCIFindingRaised records a classification of a finished head that no agent can act on.
	EventCIFindingRaised = "ci.finding_raised"
)

// CIWaitRecord is what members did about one head's wait. Reruns counts every rerun of the head, so the automatic
// reruns up to `ci_auto_rerun` and a member's own are held to one count, and each is also an event.
type CIWaitRecord struct {
	WorkOrder string `json:"work_order"`
	Head      string `json:"head"`
	CIWaiting
}

// CIFinding is what CI's classification found of a finished head that no agent can act on: an infrastructure
// conclusion that stayed after the automatic reruns, a failure the base branch's tip shares, or a check that waits
// for a person on the forge (ADR-0037). It names the check and its conclusion and holds no log. It is kept per
// head and kind, and it raises its Needs you item while its head is the pull request's head.
type CIFinding struct {
	WorkOrder  string `json:"work_order"`
	Head       string `json:"head"`
	Kind       string `json:"kind"`
	Check      string `json:"check,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
	// URL is the forge's address for the run, for a link when it is one a page may follow.
	URL string `json:"url,omitempty"`
	// BaseCommit is the base tip a red base was read at.
	BaseCommit string `json:"base_commit,omitempty"`
	RaisedAt   int64  `json:"raised_at"`
}

// FindingKind says a kind is one a classification raises.
func FindingKind(kind string) bool {
	return kind == CIItemInfrastructure || kind == CIItemBaseRed || kind == CIItemActionRequired
}

// Link is the finding's address when a page may link it.
func (f CIFinding) Link() string { return linkable(f.URL) }
