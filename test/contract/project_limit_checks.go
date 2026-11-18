package contract

import (
	"time"

	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
)

// ProjectLimitChecks are what holds a project to a daily and a weekly limit (ADR-0037, FR-SPEND-6): a tally of what
// the project's lanes were charged, kept by the ledger's UTC day, folded from each usage report, and read for the day
// and the week a moment falls in.
var ProjectLimitChecks = []Check{
	{"PJ1", "a spend tally adds by the ledger's UTC day, reads a day and a week, keeps each scope apart, and takes nothing off", checkSpendTallyByDay},
	{"PJ2", "a usage report adds only what the work order was charged since the last, so a repeat or a smaller figure adds nothing", checkUsageFoldsIntoTheDay},
}

// at is a moment in UTC, in milliseconds.
func at(year int, month time.Month, day, hour, minute, second, milli int) int64 {
	return time.Date(year, month, day, hour, minute, second, milli*int(time.Millisecond), time.UTC).UnixMilli()
}

// tally adds to a scope's spend at the moment the clock is frozen at, and reads the day and the week it falls in.
type tally struct {
	r     *Rig
	store ports.Store
}

func (t tally) addAt(moment int64, scope string, usd float64) error {
	t.r.DB.FreezeClock(moment)
	return t.store.Write(t.r.Ctx, t.r.Scope, "spend.tally", func(tx ports.WriteTx) error {
		if _, err := tx.Append(t.r.noteDraft(t.r.ID("note"))); err != nil {
			return err
		}
		return tx.Lanes().AddSpend(scope, usd)
	})
}

// sinceAt reads what the work scope was charged from a moment on, with the clock frozen at another.
func (t tally) sinceAt(clock, from int64) (float64, error) {
	t.r.DB.FreezeClock(clock)
	var total float64
	err := t.store.Read(t.r.Ctx, t.r.Scope, func(tx ports.ReadTx) error {
		var err error
		total, err = tx.Lanes().SpendSince(ledger.SpendScopeWork, from)
		return err
	})
	return total, err
}

// readAt reads what a scope was charged in the day and the week the moment falls in.
func (t tally) readAt(moment int64, scope string) (day, week float64, err error) {
	t.r.DB.FreezeClock(moment)
	err = t.store.Read(t.r.Ctx, t.r.Scope, func(tx ports.ReadTx) error {
		if day, err = tx.Lanes().SpendSince(scope, ledger.WindowStart(ledger.WindowDay, moment)); err != nil {
			return err
		}
		week, err = tx.Lanes().SpendSince(scope, ledger.WindowStart(ledger.WindowWeek, moment))
		return err
	})
	return day, week, err
}

func checkSpendTallyByDay(r *Rig) error {
	if r.DB.FreezeClock == nil {
		return nil
	}
	defer r.DB.FreezeClock(0)
	t := tally{r: r, store: r.Store()}
	wednesday := at(2026, time.November, 18, 12, 0, 0, 0)
	thursday := at(2026, time.November, 19, 1, 0, 0, 0)
	sundayEnd := at(2026, time.November, 22, 23, 59, 59, 999)
	mondayStart := at(2026, time.November, 23, 0, 0, 0, 0)
	for _, step := range []struct {
		at         int64
		scope      string
		usd        float64
		day, week  float64
		describing string
	}{
		{wednesday, ledger.SpendScopeWork, 3, 3, 3, "the first spend of a week"},
		{thursday, ledger.SpendScopeWork, 2, 2, 5, "the next day adds to the week and starts a day"},
		{thursday, ledger.SpendScopeWork, 0.5, 2.5, 5.5, "a second spend the same day adds to it"},
		{thursday, "assistant", 9, 2.5, 5.5, "another scope's spend is not the work's"},
		{sundayEnd, ledger.SpendScopeWork, 1, 1, 6.5, "the last millisecond of the week is the week's"},
		{mondayStart, ledger.SpendScopeWork, 4, 4, 4, "the first millisecond of the next week starts both over"},
	} {
		if err := t.addAt(step.at, step.scope, step.usd); err != nil {
			return err
		}
		day, week, err := t.readAt(step.at, ledger.SpendScopeWork)
		if err != nil {
			return err
		}
		if !near(day, step.day) || !near(week, step.week) {
			return Violatedf("%s: the day reads %v and the week %v, want %v and %v", step.describing, day, week, step.day, step.week)
		}
	}
	// A read counts the days from a moment on, so a read of a window that is over counts the days after it too.
	if from, err := t.sinceAt(mondayStart, ledger.WindowStart(ledger.WindowWeek, wednesday)); err != nil || !near(from, 10.5) {
		return Violatedf("the days from last week's Monday on read %v, want 10.5: %v", from, err)
	}
	if day, week, err := t.readAt(mondayStart, "assistant"); err != nil || day != 0 || week != 0 {
		return Violatedf("the assistant's scope reads %v and %v in a week it spent nothing in: %v", day, week, err)
	}
	// A tally only grows: nothing a surface adds takes spend off a day.
	for _, usd := range []float64{0, -3} {
		if err := t.addAt(mondayStart, ledger.SpendScopeWork, usd); err != nil {
			return err
		}
	}
	if day, _, err := t.readAt(mondayStart, ledger.SpendScopeWork); err != nil || !near(day, 4) {
		return Violatedf("an add of nothing or less changed the day to %v: %v", day, err)
	}
	return nil
}

// spentEver reads everything a scope was charged, which a check that moves no clock reads instead of a day, because a
// day can end while the check runs.
func (t tally) spentEver(scope string) (float64, error) {
	var total float64
	err := t.store.Read(t.r.Ctx, t.r.Scope, func(tx ports.ReadTx) error {
		var err error
		total, err = tx.Lanes().SpendSince(scope, 0)
		return err
	})
	return total, err
}

func checkUsageFoldsIntoTheDay(r *Rig) error {
	s, err := newStatusRig(r)
	if err != nil {
		return err
	}
	t := tally{r: r, store: s.store}
	report := func(usage ledger.GenerationUsage) error {
		reply, err := s.usage(usage)
		if err == nil && len(reply.Rejected) != 0 {
			return Violatedf("a usage report was refused: %+v", reply.Rejected)
		}
		return err
	}
	for _, step := range []struct {
		usage ledger.GenerationUsage
		total float64
		why   string
	}{
		{ledger.GenerationUsage{CostUSD: 1, KnownTurns: 2}, 1, "the first report adds what the work order was charged"},
		{ledger.GenerationUsage{CostUSD: 1, KnownTurns: 2}, 1, "the same report again adds nothing"},
		{ledger.GenerationUsage{CostUSD: 2.5, KnownTurns: 3}, 2.5, "a larger report adds the difference"},
		{ledger.GenerationUsage{CostUSD: 0.5, KnownTurns: 9}, 2.5, "a smaller report adds nothing, because spend only grows"},
	} {
		if err := report(step.usage); err != nil {
			return err
		}
		total, err := t.spentEver(ledger.SpendScopeWork)
		if err != nil {
			return err
		}
		if !near(total, step.total) {
			return Violatedf("%s: the tally reads %v, want %v", step.why, total, step.total)
		}
	}
	spend, err := r.spendOf(s.store, s.grant.WorkOrder)
	if err != nil || !near(spend.Charged.ChargedUSD, 2.5) {
		return Violatedf("the work order is charged %+v, and the tally is the sum of what it was charged: %v", spend.Charged, err)
	}
	return nil
}
