package ledger

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// The kinds of spend notice (FR-SPEND-19, ADR-0037). Each is a ledger event that names its scope, and a member chooses the
// channels each goes through in `[notify_policy]`, the web UI only by default.
const (
	NoticeSpendSoftLimit     = "spend_soft_limit"
	NoticeSpendHardLimit     = "spend_hard_limit"
	NoticeSpendNoProgress    = "spend_no_progress"
	NoticeSpendWaiting       = "spend_waiting"
	NoticeSpendPolicyChanged = "spend_policy_changed"
)

// SpendNoticeKinds lists the kinds in the order a surface shows them.
var SpendNoticeKinds = []string{NoticeSpendSoftLimit, NoticeSpendHardLimit, NoticeSpendNoProgress, NoticeSpendWaiting, NoticeSpendPolicyChanged}

// IsSpendNotice says a notice kind is one of the spend kinds.
func IsSpendNotice(kind string) bool { return slices.Contains(SpendNoticeKinds, kind) }

// The scopes a spend notice names. They are the reasons its sentence is chosen by, so a notice leaves the machine saying
// what kind of thing reached a limit and never which one.
const (
	NoticeScopeWorkOrder    = "work_order"
	NoticeScopeReview       = "review"
	NoticeScopePlan         = "plan"
	NoticeScopeProjectDay   = "project_day"
	NoticeScopeProjectWeek  = "project_week"
	NoticeScopeAssistant    = "assistant"
	NoticeScopeOrchestrator = "orchestrator"
	NoticeScopeLane         = "lane"
	NoticeScopeProject      = "project"
	NoticeScopeInstallation = "installation"
	NoticeScopeLimitRaised  = "limit_raised"
)

const (
	// SubjectSpendNotice and SpendNoticeSubject are what every spend notice's event is about, so the notices a channel has
	// to tell are the events of one subject and no scan of the log finds them.
	SubjectSpendNotice = "spend_notice"
	SpendNoticeSubject = "notices"
	// EventSpendNotice is the event of a spend notice. It is the record that the notice exists, and the notice's events, the
	// attempt and the word of each channel, are the record of what became of it.
	EventSpendNotice = "spend.notice"
)

// SpendKeyChange is one setting a spend notice says changed: its key, and its value before and after as `spend show` reads
// it, with a list's contents left out.
type SpendKeyChange struct {
	Key    string `json:"key"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// SpendNotice is the data of EventSpendNotice. It names its scope: the kind of thing it is about, and which one, in the
// ledger, which a notice that leaves the machine never carries.
type SpendNotice struct {
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
	// Subject names the work order, plan, lane, or conversation the scope is, and is empty for the project and the
	// installation.
	Subject string `json:"subject,omitempty"`
	// WindowStart is when the day or the week a project notice is about began, so a new day is a new notice.
	WindowStart int64 `json:"window_start,omitempty"`
	// LimitUSD is the limit reached, and ChargedUSD what was charged when it was.
	LimitUSD   float64 `json:"limit_usd,omitempty"`
	ChargedUSD float64 `json:"charged_usd,omitempty"`
	// Changes are the settings a policy notice says changed, before and after, and By is the member who changed them.
	Changes       []SpendKeyChange `json:"changes,omitempty"`
	By            string           `json:"by,omitempty"`
	PolicyVersion int64            `json:"policy_version,omitempty"`
}

// Key says which notice this is, so a limit that is reported again says nothing again (FR-SPEND-19): the same kind, scope,
// subject, window, and limit. A limit a member raised is another limit, so passing it is told again.
func (n SpendNotice) Key() string {
	return strings.Join([]string{n.Kind, n.Scope, n.Subject, strconv.FormatInt(n.WindowStart, 10), strconv.FormatFloat(n.LimitUSD, 'g', -1, 64)}, "\x00")
}

// SpendNoticeDraft is the event that records a spend notice.
func SpendNoticeDraft(n SpendNotice, actor Actor) (EventDraft, error) {
	if !IsSpendNotice(n.Kind) || n.Scope == "" {
		return EventDraft{}, fmt.Errorf("a spend notice has one of the spend kinds and names its scope: %q, %q", n.Kind, n.Scope)
	}
	data, err := json.Marshal(n)
	if err != nil {
		return EventDraft{}, err
	}
	return EventDraft{Type: EventSpendNotice, Actor: actor, Data: data, Subject: Subject{Kind: SubjectSpendNotice, ID: SpendNoticeSubject}}, nil
}

// ReadSpendNotice reads a spend notice from its event.
func ReadSpendNotice(event Event) (SpendNotice, error) {
	var n SpendNotice
	if event.Type != EventSpendNotice {
		return n, fmt.Errorf("event %s is no spend notice", event.Type)
	}
	return n, json.Unmarshal(event.Data, &n)
}
