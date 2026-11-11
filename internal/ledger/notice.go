package ledger

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

// SubjectNotice is what a notice's events are about.
const SubjectNotice = "notice"

// A notice's life is its events, and they are the only record: one attempt before a channel is asked,
// and one word after. A crash between them leaves an attempt with no word, and the next tick tries
// again, so a channel can hear a notice twice and the ledger shows that it did.
const (
	EventNoticeAttempted = "notice.attempted"
	EventNoticeDelivered = "notice.delivered"
	EventNoticeFailed    = "notice.failed"
)

// The kinds of thing a notice is about, which are the kinds of thing that wait for a person.
const (
	NoticeQuestions = "questions"
	NoticeApproval  = "approval"
	NoticeEffect    = "effect"
	NoticeWork      = "work"
)

// noticeTexts is every sentence a notice can say. A notice leaves this machine, and a ledger holds
// what agents, repositories, and trackers wrote, so nothing of that may reach this table: no title, no
// question, no name, no path, and no ID.
var noticeTexts = map[string]string{
	NoticeQuestions:              "Questions wait for your answer.",
	NoticeApproval:               "A lane waits for your approval.",
	NoticeEffect:                 "A crash left something for you to decide.",
	NoticeWork + "/lapsed":       "Work lost its worker.",
	NoticeWork + "/blocked":      "A lane stopped, and it needs you.",
	NoticeWork + "/tripped":      "A breaker stopped a lane.",
	NoticeWork + "/stalled":      "A lane shows no progress.",
	NoticeWork + "/long_parked":  "A lane has stayed parked for a long time.",
	NoticeWork + "/acceptance":   "Finished work waits for your word.",
	NoticeWork + "/human_resume": "A parked lane waits for you to resume it.",
}

const noticeFallback = "Something needs you."

// Notice is everything a channel is told. Each field is here on purpose, and docs/notifications.md
// states the list, which a test holds to this type. Delivery names the thing that waits, by a digest
// that a repeat keeps, so a receiver can drop one.
type Notice struct {
	Delivery string `json:"delivery"`
	// Project is the name of the project the thing waits in, which a member chose.
	Project string `json:"project,omitempty"`
	Attempt int64  `json:"attempt"`
	Kind    string `json:"kind"`
	Reason  string `json:"reason,omitempty"`
	Since   int64  `json:"since"`
	Text    string `json:"text"`
}

// NewNotice says that something waits, in words of this table only. A reason this table does not hold
// goes no further, so a new reason cannot carry text out by being new.
func NewNotice(scope WorkspaceID, kind, reason, id string, since int64) Notice {
	digest := sha256.Sum256([]byte(strings.Join([]string{string(scope), kind, reason, id, strconv.FormatInt(since, 10)}, "\x00")))
	notice := Notice{Delivery: hex.EncodeToString(digest[:16]), Kind: NoticeWork, Since: since, Text: noticeFallback}
	if text, ok := noticeTexts[kind]; ok {
		notice.Kind, notice.Text = kind, text
		return notice
	}
	if text, ok := noticeTexts[NoticeWork+"/"+reason]; ok {
		notice.Reason, notice.Text = reason, text
	}
	return notice
}

// In names the project a notice's thing waits in, in its fields and at the head of its text, because one
// member hears of every project of an installation (FR-PROJ-8).
func (n Notice) In(project string) Notice {
	if project == "" {
		return n
	}
	n.Project, n.Text = project, project+": "+n.Text
	return n
}

// NoticeReport is the data of a notice's events.
type NoticeReport struct {
	Delivery string `json:"delivery"`
	Channel  string `json:"channel"`
	Attempt  int64  `json:"attempt"`
	Kind     string `json:"kind"`
	Reason   string `json:"reason,omitempty"`
	Since    int64  `json:"since"`
	// Failure says why a channel did not take a notice, in a channel's own words, which hold no
	// address and no secret.
	Failure string `json:"failure,omitempty"`
}

// NoticeLine is how one notice stands on one channel. More attempts than one, with a delivery, is a
// notice a channel may have heard twice.
type NoticeLine struct {
	Delivery  string `json:"delivery"`
	Channel   string `json:"channel"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason,omitempty"`
	Since     int64  `json:"since"`
	Attempts  int64  `json:"attempts"`
	Delivered bool   `json:"delivered"`
	Failure   string `json:"failure,omitempty"`
	LastAt    int64  `json:"last_at"`
}

// ReplayNotices rebuilds how each notice stands on each channel, oldest first. Nothing else holds this,
// so a reader and the tick cannot disagree.
func ReplayNotices(events []Event) ([]NoticeLine, error) {
	byKey := map[[2]string]*NoticeLine{}
	var order [][2]string
	for _, event := range events {
		if event.Subject.Kind != SubjectNotice {
			continue
		}
		var report NoticeReport
		if err := json.Unmarshal(event.Data, &report); err != nil {
			return nil, err
		}
		key := [2]string{report.Delivery, report.Channel}
		line := byKey[key]
		if line == nil {
			line = &NoticeLine{Delivery: report.Delivery, Channel: report.Channel, Kind: report.Kind, Reason: report.Reason, Since: report.Since}
			byKey[key] = line
			order = append(order, key)
		}
		line.LastAt = event.RecordedAt
		switch event.Type {
		case EventNoticeAttempted:
			line.Attempts = max(line.Attempts, report.Attempt)
		case EventNoticeDelivered:
			line.Delivered, line.Failure = true, ""
		case EventNoticeFailed:
			line.Failure = report.Failure
		}
	}
	lines := make([]NoticeLine, 0, len(order))
	for _, key := range order {
		lines = append(lines, *byKey[key])
	}
	slices.SortStableFunc(lines, func(a, b NoticeLine) int { return cmp.Compare(a.Since, b.Since) })
	return lines, nil
}
