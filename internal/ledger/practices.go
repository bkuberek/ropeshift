package ledger

import (
	"errors"

	"ropeshift.com/ropeshift/internal/practices"
)

// PracticesRequestTTLMs is how long a practices check request is offered: 30 minutes after it was made. A first scan of
// a long history can take minutes, and a report that comes later is still recorded.
const PracticesRequestTTLMs int64 = 30 * 60 * 1000

// PracticesCause is what made a practices check request.
type PracticesCause string

// The causes of a request.
const (
	// PracticesRegistration is the registration of a repository, by `repository add`, an adoption, or a manifest.
	PracticesRegistration PracticesCause = "registration"
	// PracticesPlanningSession is the start of a planning session.
	PracticesPlanningSession PracticesCause = "planning_session"
	// PracticesMember is a member's or a delegate's request.
	PracticesMember PracticesCause = "member"
)

// Valid says the cause is one of the three.
func (c PracticesCause) Valid() bool {
	switch c {
	case PracticesRegistration, PracticesPlanningSession, PracticesMember:
		return true
	}
	return false
}

// PracticesState is where a practices check request stands.
type PracticesState string

// The states of a request. A request is offered until its worker reports it. One nobody reported by its expiry
// becomes expired on the next pass of the control plane's tick, and a report that comes later is still recorded.
const (
	PracticesOffered  PracticesState = "offered"
	PracticesReported PracticesState = "reported"
	PracticesExpired  PracticesState = "expired"
)

// The outcomes of a worker's practices check.
const (
	PracticesOutcomeChecked = "checked"
	PracticesOutcomeFailed  = "failed"
)

// MaxPracticesReason is the most bytes of the reason a member gives for acknowledging a finding: 2 KiB.
const MaxPracticesReason = 2048

// The subjects and the events of the practices check.
const (
	SubjectPracticesRequest = "practices_request"
	// SubjectPracticesFinding is one repository's finding, which a member acknowledges and withdraws. Its ID is the
	// repository's ID and the finding's ID, joined by ':'.
	SubjectPracticesFinding = "practices_finding"

	EventPracticesRequested = "practices.requested"
	EventPracticesChecked   = "practices.checked"
	EventPracticesFailed    = "practices.failed"
	EventPracticesExpired   = "practices.expired"

	EventPracticesAcknowledged = "practices.acknowledged"
	EventPracticesWithdrawn    = "practices.withdrawn"
)

// ErrAcknowledged means a repository already holds an acknowledgement of the finding, which a member withdraws before
// acknowledging it again.
var ErrAcknowledged = errors.New("the finding is already acknowledged")

// PracticesFindingSubject is the subject of a repository's finding.
func PracticesFindingSubject(repository, finding string) Subject {
	return Subject{Kind: SubjectPracticesFinding, ID: repository + ":" + finding}
}

// PracticesRequest is a request that the worker holding a repository's checkout run the practices check, and
// where it stands. A member reads it as it is.
type PracticesRequest struct {
	ID          string         `json:"id"`
	Repository  string         `json:"repository"`
	Worker      string         `json:"worker"`
	Cause       PracticesCause `json:"cause"`
	RequestedBy string         `json:"requested_by"`
	RequestedAt int64          `json:"requested_at"`
	ExpiresAt   int64          `json:"expires_at"`
	// State is offered until a report lands, and then reported. A request nobody reported by its expiry is stored as
	// expired by the tick, and StateAt reads it so between the expiry and the tick.
	State      PracticesState `json:"state"`
	ReportedAt int64          `json:"reported_at,omitempty"`
}

// StateAt is the request's state at a time: offered becomes expired once its expiry passes with no report.
func (r PracticesRequest) StateAt(now int64) PracticesState {
	if r.State == PracticesOffered && now >= r.ExpiresAt {
		return PracticesExpired
	}
	return r.State
}

// Open says the request is offered and has not expired at a time.
func (r PracticesRequest) Open(now int64) bool { return r.StateAt(now) == PracticesOffered }

// PracticesRecord is the latest result of one repository, and the request that made it.
type PracticesRecord struct {
	Repository string
	Request    string
	// Commit is the commit the check read.
	Commit    string
	Result    practices.Result
	CheckedAt int64
}

// PracticesScan is where a repository's last completed secret scan ended: the commit it read, and the version of the
// credential rule set it matched with.
type PracticesScan struct {
	Tip          string
	RulesVersion int
}

// From is the commit the next scan of the repository starts after, or empty to scan the whole history. A scan that
// matched with another version of the credential rule set than this build's holds no commit the next scan may skip:
// the commits it read were never matched against the rules in force.
func (s PracticesScan) From() string {
	if s.RulesVersion != CredentialRulesVersion {
		return ""
	}
	return s.Tip
}

// PracticesAcknowledgement is a member's reason for leaving a repository's finding open. The finding stays in every
// view of the result, marked with the reason, the member, and the time, until the member withdraws it.
type PracticesAcknowledgement struct {
	Repository     string
	Finding        string
	Reason         string
	Member         string
	AcknowledgedAt int64
}

// DesiredPracticesCheck is a practices check request a worker has not reported. The control plane offers it to the
// worker it names, until the worker reports it or it expires.
type DesiredPracticesCheck struct {
	ID string `json:"id"`
	// Project names the project the repository is in, as every item of a plane that serves several projects does.
	Project    string `json:"project,omitempty"`
	Repository string `json:"repository"`
	// BaseBranch is the branch the check reads.
	BaseBranch string `json:"base_branch"`
	// Binding is the repository's forge binding as HOST/OWNER/REPO, empty for none.
	Binding        string `json:"binding,omitempty"`
	MergeAuthority string `json:"merge_authority"`
	// ScannedFrom is the commit the last completed secret scan of the repository read, empty to scan the whole
	// history.
	ScannedFrom string `json:"scanned_from,omitempty"`
	ExpiresAt   int64  `json:"expires_at"`
}

// PracticesReport is what a worker did with a practices check request: the result it came to, or why it could not
// run.
type PracticesReport struct {
	Auth WorkerAuth `json:"-"`
	// Project names the project the request is in, as the desired check did, so a plane that serves several projects
	// records it in the right one.
	Project string `json:"project,omitempty"`
	Request string `json:"request"`
	Outcome string `json:"outcome"`
	// Detail says why a failed check did not run, on one line and bounded, with no path of the worker's machine.
	Detail string `json:"detail,omitempty"`
	// Result is what a check came to, for the outcome checked.
	Result *practices.Result `json:"result,omitempty"`
}
