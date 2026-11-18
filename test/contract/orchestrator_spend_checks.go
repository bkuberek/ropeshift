package contract

import (
	"encoding/json"
	"time"

	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// orchestratorReport is one machine's orchestrator telling the ledger what a generation has spent.
type orchestratorReport struct {
	r       *Rig
	s       *statusRig
	auth    ledger.WorkerAuth
	epoch   int64
	lane    string
	seq     int64
	spend   *control.Spend
	version int64
}

func (o *orchestratorReport) spent(generation int64, usage ledger.GenerationUsage) (ledger.UploadReply, error) {
	raw, err := json.Marshal(usage)
	if err != nil {
		return ledger.UploadReply{}, err
	}
	o.seq++
	return o.s.plane.Upload(o.r.Ctx, ledger.UploadBatch{Auth: o.auth, Epoch: o.epoch, Events: []ledger.UploadEvent{{
		Epoch: o.epoch, Seq: o.seq, Type: ledger.UploadGenerationUsage, Lane: o.lane, Generation: generation, Payload: raw, OccurredAt: 1,
	}}})
}

// warn publishes the installation's soft daily limit for the orchestrator.
func (o *orchestratorReport) warn(usd float64) error {
	o.version++
	policy := authority.DefaultInstallationSpendPolicy()
	policy.Version, policy.OrchestratorDailyWarn = o.version, usd
	_, err := o.spend.PublishInstallationSpendPolicy(o.r.Ctx, o.r.Scope, authority.InstallationSpendPolicyDraft{Body: policy.Body(), Policy: policy}, theMember)
	return err
}

// day reads what an orchestrator's machine was charged from a moment on.
func (o *orchestratorReport) day(from int64) (float64, error) {
	var total float64
	err := o.s.store.Installation(o.r.Scope).Read(o.r.Ctx, func(tx ports.SharedReadTx) error {
		var err error
		total, err = tx.Spend().SpendSince(ledger.OrchestratorSpendScope(o.auth.WorkerID), from)
		return err
	})
	return total, err
}

func checkTheOrchestratorsDailyWarning(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	started, err := orchestrating(s)
	if err != nil {
		return err
	}
	a := &orchestratorReport{r: r, s: s, auth: s.auth, epoch: s.epoch, lane: started.call.Lane, seq: s.seq, spend: control.NewSpend(s.store), version: 1}
	// A second machine's orchestrator runs under the same lane name, and spends in a tally of its own.
	token, err := s.plane.CreateJoinToken(r.Ctx, theMember)
	if err != nil {
		return err
	}
	other := r.ID("worker")
	credential, err := s.plane.Register(r.Ctx, ledger.Registration{JoinToken: token, WorkerID: other, Name: "basalt"})
	if err != nil {
		return err
	}
	otherAuth := ledger.WorkerAuth{WorkerID: other, Credential: credential.Credential}
	session, err := s.plane.OpenSession(r.Ctx, ledger.SessionRequest{Auth: otherAuth, Protocol: ledger.ProtocolVersion, Manifest: UsableManifest})
	if err != nil {
		return err
	}
	if _, err := s.plane.SetOrchestrator(r.Ctx, other, ledger.OrchestratorRunning, theMember); err != nil {
		return err
	}
	b := &orchestratorReport{
		r: r, s: s, auth: otherAuth, epoch: session.Epoch, lane: ledger.Orchestrator{ID: ledger.OrchestratorOf(other), Run: 1}.Lane(),
		spend: a.spend, version: 1,
	}
	status, err := json.Marshal(ledger.LaneStatus{ActualState: "working", Liveness: "alive", ModelReach: "reachable", LedgerReach: "reachable"})
	if err != nil {
		return err
	}
	b.seq++
	if err := accepted(s.plane.Upload(r.Ctx, ledger.UploadBatch{Auth: b.auth, Epoch: b.epoch, Events: []ledger.UploadEvent{{
		Epoch: b.epoch, Seq: b.seq, Type: ledger.UploadLaneStatus, Lane: b.lane, Generation: 1, Payload: status, OccurredAt: 1,
	}}})); err != nil {
		return err
	}
	if err := a.warn(5); err != nil {
		return err
	}
	if r.DB.FreezeClock != nil {
		defer r.DB.FreezeClock(0)
		r.DB.FreezeClock(at(2026, time.November, 18, 12, 0, 0, 0))
	}
	warned := func() ([]ledger.SpendNotice, error) {
		notices, err := r.spendNotices(s.store, ledger.NoticeSpendSoftLimit)
		return ofScope(notices, ledger.NoticeScopeOrchestrator), err
	}
	for _, step := range []struct {
		generation int64
		usd, total float64
		notices    int
	}{{1, 3, 3, 0}, {1, 5.5, 5.5, 1}, {1, 6, 6, 1}, {1, 6, 6, 1}, {1, 2, 6, 1}, {2, 1, 7, 1}} {
		if reply, err := a.spent(step.generation, ledger.GenerationUsage{CostUSD: step.usd, KnownTurns: 2}); accepted(reply, err) != nil {
			return Violatedf("a report of %v was refused: %+v, %v", step.usd, reply.Rejected, err)
		}
		if got, err := a.day(0); err != nil || !near(got, step.total) {
			return Violatedf("after a report of %v in generation %d the machine's day reads %v, want %v: %v", step.usd, step.generation, got, step.total, err)
		}
		if got, err := warned(); err != nil || len(got) != step.notices {
			return Violatedf("after a report of %v the orchestrator has %d warnings, want %d: %v", step.usd, len(got), step.notices, err)
		}
	}
	got, err := warned()
	if err != nil || !near(got[0].LimitUSD, 5) || !near(got[0].ChargedUSD, 5.5) || got[0].Subject != a.auth.WorkerID {
		return Violatedf("the warning reads %+v: %v", got, err)
	}
	// Another machine's orchestrator has a tally of its own, under the same lane name, and its own warning.
	if reply, err := b.spent(1, ledger.GenerationUsage{CostUSD: 4, KnownTurns: 1}); accepted(reply, err) != nil {
		return Violatedf("a report of the other machine was refused: %+v, %v", reply.Rejected, err)
	}
	if mine, err := a.day(0); err != nil || !near(mine, 7) {
		return Violatedf("the other machine's spend moved this one's day to %v: %v", mine, err)
	}
	if theirs, err := b.day(0); err != nil || !near(theirs, 4) {
		return Violatedf("the other machine's day reads %v, want 4: %v", theirs, err)
	}
	if got, err := warned(); err != nil || len(got) != 1 {
		return Violatedf("4 spent on a machine with a warning at 5 told %+v: %v", got, err)
	}
	if reply, err := b.spent(1, ledger.GenerationUsage{CostUSD: 5, KnownTurns: 1}); accepted(reply, err) != nil {
		return Violatedf("the other machine's second report was refused: %+v, %v", reply.Rejected, err)
	}
	if got, err := warned(); err != nil || len(got) != 2 || got[0].Subject == got[1].Subject {
		return Violatedf("two machines over their warning told %+v, want one notice each: %v", got, err)
	}
	// A report that spends nothing, or a figure below one told, is no spend.
	if reply, err := a.spent(1, ledger.GenerationUsage{CostUSD: -1}); err != nil || len(reply.Rejected) != 1 || reply.Rejected[0].Status != ledger.UploadInvalid {
		return Violatedf("a negative report: %+v, %v", reply, err)
	}
	// The next UTC day is a new day: its first report under the warning tells nothing, and the one that reaches it tells again.
	if r.DB.FreezeClock != nil {
		r.DB.FreezeClock(at(2026, time.November, 19, 0, 30, 0, 0))
		if reply, err := a.spent(2, ledger.GenerationUsage{CostUSD: 3, KnownTurns: 2}); accepted(reply, err) != nil {
			return Violatedf("a report of the next day was refused: %+v, %v", reply.Rejected, err)
		}
		if day, err := a.day(ledger.WindowStart(ledger.WindowDay, at(2026, time.November, 19, 0, 30, 0, 0))); err != nil || !near(day, 2) {
			return Violatedf("the next day reads %v, want 2: %v", day, err)
		}
		if got, err := warned(); err != nil || len(got) != 2 {
			return Violatedf("2 spent on a new day told %+v: %v", got, err)
		}
		if reply, err := a.spent(2, ledger.GenerationUsage{CostUSD: 7, KnownTurns: 2}); accepted(reply, err) != nil {
			return Violatedf("the next day's second report was refused: %+v, %v", reply.Rejected, err)
		}
		if got, err := warned(); err != nil || len(got) != 3 {
			return Violatedf("a new day that reached the warning told %+v, want a third notice: %v", got, err)
		}
	}
	// A warning of 0 is none.
	if err := a.warn(0); err != nil {
		return err
	}
	before, err := warned()
	if err != nil {
		return err
	}
	if reply, err := a.spent(3, ledger.GenerationUsage{CostUSD: 50, KnownTurns: 2}); accepted(reply, err) != nil {
		return Violatedf("a report with no warning set was refused: %+v, %v", reply.Rejected, err)
	}
	if got, err := warned(); err != nil || len(got) != len(before) {
		return Violatedf("an orchestrator with no warning set told %+v: %v", got, err)
	}
	return nil
}
