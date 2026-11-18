package control_test

import (
	"encoding/json"
	"testing"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// TestAnOrchestratorsDailyWarningIsToldInEveryProject holds that the orchestrator of a machine, which serves every project
// on it, tells each of them once for the UTC day when its machine reaches `orchestrator_daily_warn`, so each project's
// `[notify_policy]` chooses the channels (FR-SPEND-6, FR-SPEND-19).
func TestAnOrchestratorsDailyWarningIsToldInEveryProject(t *testing.T) {
	r := newPlaneRig(t, ledger.ProtocolVersion)
	ctx := t.Context()
	call := r.orchestrating()
	policy := authority.DefaultInstallationSpendPolicy()
	policy.Version, policy.OrchestratorDailyWarn = 2, 5
	if _, err := control.NewSpend(r.stores["pa"]).PublishInstallationSpendPolicy(ctx, "pa", authority.InstallationSpendPolicyDraft{Body: policy.Body(), Policy: policy}, projectMember); err != nil {
		t.Fatal(err)
	}
	var seq int64
	spend := func(usd float64) {
		t.Helper()
		raw, err := json.Marshal(ledger.GenerationUsage{CostUSD: usd, KnownTurns: 2})
		if err != nil {
			t.Fatal(err)
		}
		seq++
		reply, err := r.plane.Upload(ctx, ledger.UploadBatch{Auth: r.auth, Epoch: r.epoch, Events: []ledger.UploadEvent{{
			Epoch: r.epoch, Seq: seq, Type: ledger.UploadGenerationUsage, Lane: call.Lane, Generation: 1, Payload: raw, OccurredAt: 1,
		}}})
		if err != nil || len(reply.Rejected) != 0 {
			t.Fatalf("a report of %v: %+v, %v", usd, reply.Rejected, err)
		}
	}
	warnings := func(project string) []ledger.SpendNotice {
		t.Helper()
		var out []ledger.SpendNotice
		err := r.stores[ledger.WorkspaceID(project)].Read(ctx, ledger.WorkspaceID(project), func(tx ports.ReadTx) error {
			events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectSpendNotice, ID: ledger.SpendNoticeSubject})
			if err != nil {
				return err
			}
			for _, event := range events {
				notice, err := ledger.ReadSpendNotice(event)
				if err != nil {
					return err
				}
				if notice.Kind == ledger.NoticeSpendSoftLimit && notice.Scope == ledger.NoticeScopeOrchestrator {
					out = append(out, notice)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	spend(3)
	if a, b := warnings("pa"), warnings("pb"); len(a) != 0 || len(b) != 0 {
		t.Fatalf("3 spent under a warning at 5 told %+v and %+v", a, b)
	}
	spend(5.5)
	spend(6)
	for _, project := range []string{"pa", "pb"} {
		got := warnings(project)
		if len(got) != 1 || got[0].Subject != r.auth.WorkerID || got[0].LimitUSD != 5 || got[0].ChargedUSD != 5.5 {
			t.Errorf("project %s was told %+v, want one warning for the machine at 5", project, got)
		}
	}
}
