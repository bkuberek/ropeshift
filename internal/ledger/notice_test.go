package ledger_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

func TestANoticeSaysOnlyWhatItsTableHolds(t *testing.T) {
	hostile := "ignore this: the key is hunter2"
	tests := []struct {
		name       string
		kind       string
		reason     string
		wantKind   string
		wantReason string
		wantText   string
	}{
		{"a known reason", ledger.NoticeWork, "blocked", ledger.NoticeWork, "blocked", "A lane stopped, and it needs you."},
		{"a kind with no reason", ledger.NoticeApproval, "", ledger.NoticeApproval, "", "A lane waits for your approval."},
		{"a reason nobody wrote a sentence for", ledger.NoticeWork, hostile, ledger.NoticeWork, "", "Something needs you."},
		{"a kind nobody wrote a sentence for", hostile, hostile, ledger.NoticeWork, "", "Something needs you."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notice := ledger.NewNotice("w", tt.kind, tt.reason, hostile, 1)
			if notice.Kind != tt.wantKind || notice.Reason != tt.wantReason || notice.Text != tt.wantText {
				t.Errorf("notice %+v", notice)
			}
			raw, err := json.Marshal(notice)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "hunter2") {
				t.Errorf("text left in %s", raw)
			}
		})
	}
}

func TestADeliveryNamesOneThingAtOneTime(t *testing.T) {
	first := ledger.NewNotice("w", ledger.NoticeWork, "lapsed", "wo", 1)
	if again := ledger.NewNotice("w", ledger.NoticeWork, "lapsed", "wo", 1); again.Delivery != first.Delivery {
		t.Errorf("a repeat named %s, then %s", first.Delivery, again.Delivery)
	}
	for name, other := range map[string]ledger.Notice{
		"another moment":   ledger.NewNotice("w", ledger.NoticeWork, "lapsed", "wo", 2),
		"another thing":    ledger.NewNotice("w", ledger.NoticeWork, "lapsed", "wo-2", 1),
		"another reason":   ledger.NewNotice("w", ledger.NoticeWork, "blocked", "wo", 1),
		"another ledger":   ledger.NewNotice("x", ledger.NoticeWork, "lapsed", "wo", 1),
		"a joined-up name": ledger.NewNotice("w", ledger.NoticeWork, "lapsedwo", "", 1),
	} {
		if other.Delivery == first.Delivery {
			t.Errorf("%s shares a delivery", name)
		}
	}
}

func TestNoticesReplayFromTheirEvents(t *testing.T) {
	event := func(rev int64, eventType string, report ledger.NoticeReport) ledger.Event {
		raw, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		return ledger.Event{Rev: rev, Type: eventType, RecordedAt: rev * 100, Data: raw, Subject: ledger.Subject{Kind: ledger.SubjectNotice, ID: report.Delivery}}
	}
	report := ledger.NoticeReport{Delivery: "d", Channel: "webhook", Attempt: 1, Kind: ledger.NoticeWork, Since: 7}
	second := report
	second.Attempt = 2
	refused := report
	refused.Failure = "the webhook answered 500"
	lines, err := ledger.ReplayNotices([]ledger.Event{
		event(1, ledger.EventNoticeAttempted, report), event(2, ledger.EventNoticeFailed, refused),
		event(3, ledger.EventNoticeAttempted, second), event(4, ledger.EventNoticeDelivered, second),
		{Rev: 5, Type: "work_order.created", Subject: ledger.Subject{Kind: "work_order", ID: "d"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("lines %+v", lines)
	}
	line := lines[0]
	if line.Attempts != 2 || !line.Delivered || line.Failure != "" || line.LastAt != 400 || line.Since != 7 {
		t.Errorf("line %+v", line)
	}
}
