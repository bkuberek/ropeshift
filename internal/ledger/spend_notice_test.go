package ledger_test

import (
	"encoding/json"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/ledger"
)

// Each spend notice kind says one fixed sentence for the scope it names, and a reason nobody wrote a sentence for says
// the sentence of the kind and no more, so a scope that is new cannot carry text out by being new (FR-SPEND-19).
func TestASpendNoticeSaysOneSentenceForItsScope(t *testing.T) {
	hostile := "wo-ignore this: the key is hunter2"
	for _, tt := range []struct {
		kind, reason, want string
	}{
		{ledger.NoticeSpendSoftLimit, ledger.NoticeScopeWorkOrder, "A work order reached its soft spend limit."},
		{ledger.NoticeSpendSoftLimit, ledger.NoticeScopeReview, "A review reached its soft spend limit."},
		{ledger.NoticeSpendSoftLimit, ledger.NoticeScopePlan, "A plan reached its soft spend limit."},
		{ledger.NoticeSpendSoftLimit, ledger.NoticeScopeProjectDay, "The project reached its soft daily spend limit."},
		{ledger.NoticeSpendSoftLimit, ledger.NoticeScopeProjectWeek, "The project reached its soft weekly spend limit."},
		{ledger.NoticeSpendSoftLimit, ledger.NoticeScopeAssistant, "The assistant reached its daily spend warning."},
		{ledger.NoticeSpendSoftLimit, ledger.NoticeScopeOrchestrator, "The orchestrator reached its daily spend warning."},
		{ledger.NoticeSpendHardLimit, ledger.NoticeScopeWorkOrder, "A work order reached its hard spend limit, and its lane parked."},
		{ledger.NoticeSpendHardLimit, ledger.NoticeScopeReview, "A review reached its hard spend limit, and its lane parked."},
		{ledger.NoticeSpendHardLimit, ledger.NoticeScopePlan, "A plan reached its hard spend limit, and none of its work starts."},
		{ledger.NoticeSpendHardLimit, ledger.NoticeScopeProjectDay, "The project reached its daily spend limit, and no work starts until the day ends or a member raises it."},
		{ledger.NoticeSpendHardLimit, ledger.NoticeScopeProjectWeek, "The project reached its weekly spend limit, and no work starts until the week ends or a member raises it."},
		{ledger.NoticeSpendNoProgress, ledger.NoticeScopeLane, "A lane parked because it made no progress."},
		{ledger.NoticeSpendWaiting, ledger.NoticeScopeLane, "A lane spends most of its calls waiting."},
		{ledger.NoticeSpendPolicyChanged, ledger.NoticeScopeProject, "The project's spend settings changed."},
		{ledger.NoticeSpendPolicyChanged, ledger.NoticeScopeInstallation, "The installation's spend settings changed."},
		{ledger.NoticeSpendPolicyChanged, ledger.NoticeScopeLimitRaised, "A spend limit was raised."},
		// A reason nobody wrote a sentence for keeps the kind, drops the reason, and says the kind's own.
		{ledger.NoticeSpendHardLimit, hostile, "A spend limit was reached."},
		{ledger.NoticeSpendSoftLimit, "", "A spend limit was nearly reached."},
		{ledger.NoticeSpendNoProgress, hostile, "A lane parked because it made no progress."},
		{ledger.NoticeSpendWaiting, "", "A lane spends most of its calls waiting."},
		{ledger.NoticeSpendPolicyChanged, hostile, "A spend setting changed."},
	} {
		notice := ledger.NewNotice("w", tt.kind, tt.reason, hostile, 1)
		if notice.Kind != tt.kind || notice.Text != tt.want {
			t.Errorf("%s for %q reads %+v, want %q", tt.kind, tt.reason, notice, tt.want)
		}
		if notice.Reason != "" && notice.Reason != tt.reason {
			t.Errorf("%s for %q names the reason %q", tt.kind, tt.reason, notice.Reason)
		}
		raw, err := json.Marshal(notice)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "wo-ignore") {
			t.Errorf("text left in %s", raw)
		}
	}
	if len(ledger.SpendNoticeKinds) != 5 {
		t.Errorf("the spend notice kinds are %v, want the five of FR-SPEND-19", ledger.SpendNoticeKinds)
	}
	for _, kind := range ledger.SpendNoticeKinds {
		if !ledger.IsSpendNotice(kind) {
			t.Errorf("%s is not read as a spend notice", kind)
		}
	}
	if ledger.IsSpendNotice(ledger.NoticeWork) || ledger.IsSpendNotice(ledger.CIItemNotFinished) {
		t.Error("a notice of another sort reads as a spend notice")
	}
}

// A spend notice's event names its scope and what it needs to be read and to be told once: the kind, the scope, the limit
// it reached or the keys it changed and who changed them, and nothing a lane or a repository wrote.
func TestASpendNoticeEventNamesItsScope(t *testing.T) {
	draft, err := ledger.SpendNoticeDraft(ledger.SpendNotice{
		Kind: ledger.NoticeSpendHardLimit, Scope: ledger.NoticeScopeProjectDay, WindowStart: 1_000, LimitUSD: 10, ChargedUSD: 10.4,
	}, ledger.Actor{Kind: ledger.ActorSystem, ID: "spend"})
	if err != nil {
		t.Fatal(err)
	}
	if draft.Type != ledger.EventSpendNotice || draft.Subject != (ledger.Subject{Kind: ledger.SubjectSpendNotice, ID: ledger.SpendNoticeSubject}) {
		t.Errorf("the draft is %+v", draft)
	}
	notice, err := ledger.ReadSpendNotice(ledger.Event{Type: draft.Type, Data: draft.Data})
	if err != nil || notice.Kind != ledger.NoticeSpendHardLimit || notice.Scope != ledger.NoticeScopeProjectDay || notice.LimitUSD != 10 {
		t.Errorf("the event reads %+v: %v", notice, err)
	}
	if _, err := ledger.SpendNoticeDraft(ledger.SpendNotice{Kind: ledger.NoticeWork, Scope: ledger.NoticeScopeLane}, ledger.Actor{}); err == nil {
		t.Error("a notice of a kind that is not a spend kind was recorded as one")
	}
	if _, err := ledger.SpendNoticeDraft(ledger.SpendNotice{Kind: ledger.NoticeSpendHardLimit}, ledger.Actor{}); err == nil {
		t.Error("a spend notice that names no scope was recorded")
	}
}

// Once per limit and scope: a notice is the same one for the same kind, scope, subject, window, and limit, and another
// for a raised limit, another day, or another subject.
func TestASpendNoticeIsOnePerLimitAndScope(t *testing.T) {
	base := ledger.SpendNotice{Kind: ledger.NoticeSpendSoftLimit, Scope: ledger.NoticeScopeProjectDay, WindowStart: 1_000, LimitUSD: 8}
	if base.Key() != (ledger.SpendNotice{Kind: base.Kind, Scope: base.Scope, WindowStart: 1_000, LimitUSD: 8, ChargedUSD: 12}).Key() {
		t.Error("what was charged when the limit was reached makes another notice")
	}
	for name, other := range map[string]ledger.SpendNotice{
		"a raised limit":  {Kind: base.Kind, Scope: base.Scope, WindowStart: 1_000, LimitUSD: 16},
		"another day":     {Kind: base.Kind, Scope: base.Scope, WindowStart: 2_000, LimitUSD: 8},
		"another scope":   {Kind: base.Kind, Scope: ledger.NoticeScopeProjectWeek, WindowStart: 1_000, LimitUSD: 8},
		"another subject": {Kind: base.Kind, Scope: base.Scope, Subject: "wo-1", WindowStart: 1_000, LimitUSD: 8},
		"another kind":    {Kind: ledger.NoticeSpendHardLimit, Scope: base.Scope, WindowStart: 1_000, LimitUSD: 8},
	} {
		if base.Key() == other.Key() {
			t.Errorf("%s is the same notice", name)
		}
	}
}
