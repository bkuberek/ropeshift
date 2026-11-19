package contract

import (
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// The reports of one generation whose charge is not the same as the highest of each figure. The first charges 12, and
// the second, which carries a lower cost and one more known turn and an unknown one, charges 9. The highest of each
// figure is a cost of 12 over two known turns with one unknown, which charges 18.
var (
	costlyReport    = ledger.GenerationUsage{CostUSD: 12, KnownTurns: 1}
	manyTurnsReport = ledger.GenerationUsage{CostUSD: 6, KnownTurns: 2, UnknownTurns: 1}
	smallerReport   = ledger.GenerationUsage{CostUSD: 1, KnownTurns: 1}
)

func checkTheAssistantsSpendKeepsTheHighestOfEachFigure(r *Rig) error {
	t, err := newTalkRig(r)
	if err != nil {
		return err
	}
	opened, err := t.create(theMember, "")
	if err != nil {
		return err
	}
	tally := func() (float64, error) {
		var total float64
		err := t.store.Read(r.Ctx, r.Scope, func(tx ports.ReadTx) error {
			var err error
			total, err = tx.Lanes().SpendSince(ledger.SpendScopeAssistant, 0)
			return err
		})
		return total, err
	}
	for _, step := range []struct {
		usage ledger.GenerationUsage
		total float64
	}{{costlyReport, 12}, {manyTurnsReport, 18}, {smallerReport, 18}, {costlyReport, 18}} {
		if err := t.runUpload(opened.Conversation, ledger.UploadGenerationUsage, 1, step.usage); err != nil {
			return err
		}
		if got, err := tally(); err != nil || !near(got, step.total) {
			return Violatedf("after a report of %+v the assistant's day reads %v, want %v: %v", step.usage, got, step.total, err)
		}
	}
	return nil
}

func checkTheOrchestratorsSpendKeepsTheHighestOfEachFigure(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	started, err := orchestrating(s)
	if err != nil {
		return err
	}
	a := &orchestratorReport{r: r, s: s, auth: s.auth, epoch: s.epoch, lane: started.call.Lane, seq: s.seq, spend: control.NewSpend(s.store), version: 1}
	for _, step := range []struct {
		usage ledger.GenerationUsage
		total float64
	}{{costlyReport, 12}, {manyTurnsReport, 18}, {smallerReport, 18}, {costlyReport, 18}} {
		if reply, err := a.spent(1, step.usage); accepted(reply, err) != nil {
			return Violatedf("a report of %+v was refused: %+v, %v", step.usage, reply.Rejected, err)
		}
		if got, err := a.day(0); err != nil || !near(got, step.total) {
			return Violatedf("after a report of %+v the machine's day reads %v, want %v: %v", step.usage, got, step.total, err)
		}
	}
	return nil
}
