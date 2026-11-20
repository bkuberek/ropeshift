package contract

import (
	"errors"
	"slices"
	"strings"
	"time"

	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

// CIAttentionChecks are what CI asks of a person (ADR-0037): CI that has not finished, a run no runner picked up,
// and the findings of the classification. Each is derived from what the ledger holds of the pull request and the
// database's clock, a member's word on it is recorded, and none of it starts an agent.
var CIAttentionChecks = []Check{
	{"CI6", "a run queued past its limit raises 'No runner picked it up' and never 'CI has not finished' first, and starts no lane", checkCIQueueStalls},
	{"CI7", "a run past the CI timeout raises one item, Wait longer extends it once per press, Rerun is recorded, and no action starts an agent", checkCITimeoutAndItsAnswers},
	{"CI8", "editing ci_timeout_multiplier and ci_queue_timeout_no_history moves the times the items fire at", checkCIEditsMoveTheItems},
	{"CI9", "a finding of the classification raises its item once while its head stands, a rerun clears it, and a delegate answers nothing", checkCIFindings},
}

// ciCheck is a required check at a point of its run, with the address of the run.
func ciCheck(status ledger.CIRunStatus) ledger.PullRequestCheck {
	return ledger.PullRequestCheck{
		Name: "unit", Required: true, State: ledger.CheckPending, Status: status, DetailsURI: "https://github.com/acme/widgets/actions/runs/9",
	}
}

// ciItems is what CI asks of a person now.
func (f *forgeRig) ciItems() ([]control.CIItem, error) {
	var items []control.CIItem
	err := f.store.Read(f.r.Ctx, f.r.Scope, func(tx ports.ReadTx) error {
		var err error
		items, err = control.CIItems(tx)
		return err
	})
	return items, err
}

// ciWatch is where the rig's pull request stands against its repository's CI limits now.
func (f *forgeRig) ciWatch() (ledger.CIWatch, error) {
	var watch *ledger.CIWatch
	err := f.store.Read(f.r.Ctx, f.r.Scope, func(tx ports.ReadTx) error {
		var err error
		watch, err = control.CIWatchOf(tx, f.grant.WorkOrder)
		return err
	})
	if err == nil && watch == nil {
		return ledger.CIWatch{}, Violatedf("the pull request is not watched")
	}
	if err != nil {
		return ledger.CIWatch{}, err
	}
	return *watch, nil
}

// agentEvents counts the events that lanes and their generations leave, which a thing that starts an agent adds to.
func (f *forgeRig) agentEvents() (int, error) {
	count := 0
	err := f.store.Read(f.r.Ctx, f.r.Scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, event := range events {
			if strings.HasPrefix(event.Type, "lane.") || strings.HasPrefix(event.Type, "generation.") {
				count++
			}
		}
		return err
	})
	return count, err
}

// startsNothing runs an act and says whether it left the lanes, the generations, and what a worker is asked to start
// as they were.
func (f *forgeRig) startsNothing(act func() error) error {
	before, err := f.agentEvents()
	if err != nil {
		return err
	}
	asked, err := f.desired()
	if err != nil {
		return err
	}
	if err := act(); err != nil {
		return err
	}
	after, err := f.agentEvents()
	if err != nil {
		return err
	}
	later, err := f.desired()
	if err != nil {
		return err
	}
	if after != before || len(later.Leases) != len(asked.Leases) || len(later.Woken) != len(asked.Woken) {
		return Violatedf("a word on CI changed the agents: %d lane events became %d, %d leases %d, %d woken %d",
			before, after, len(asked.Leases), len(later.Leases), len(asked.Woken), len(later.Woken))
	}
	return nil
}

func (f *forgeRig) oneItem(want string) (control.CIItem, error) {
	items, err := f.ciItems()
	if err != nil {
		return control.CIItem{}, err
	}
	if len(items) != 1 || items[0].Kind != want {
		return control.CIItem{}, Violatedf("CI raises %d items %+v, want one %s", len(items), kinds(items), want)
	}
	return items[0], nil
}

func kinds(items []control.CIItem) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Kind)
	}
	return out
}

func (f *forgeRig) noItems(when string) error {
	items, err := f.ciItems()
	if err != nil || len(items) != 0 {
		return Violatedf("%s, CI raises %v: %v", when, kinds(items), err)
	}
	return nil
}

// ciRig is a pull request open at the rig's head, whose one required check is at a point of its run.
func ciRig(r *Rig, status ledger.CIRunStatus, settings func(ledger.CISettings) (ledger.CISettings, error)) (*forgeRig, error) {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return nil, err
	}
	if settings != nil {
		edited, err := settings(ledger.DefaultCISettings())
		if err != nil {
			return nil, err
		}
		if _, err := r.change(f.store, f.repository, control.RepositoryChange{CI: &edited}, r.Actor); err != nil {
			return nil, err
		}
	}
	if err := f.report(f.opened(1, f.head)); err != nil {
		return nil, err
	}
	if err := f.ciRead(f.head, ciCheck(status)); err != nil {
		return nil, err
	}
	return f, nil
}

func checkCIQueueStalls(r *Rig) error {
	if r.DB.ShiftClock == nil {
		return nil
	}
	f, err := ciRig(r, ledger.CIRunQueued, func(s ledger.CISettings) (ledger.CISettings, error) {
		return s.Set("ci_timeout_no_history", "10m")
	})
	if err != nil {
		return err
	}
	return f.startsNothing(func() error {
		r.DB.ShiftClock((29 * time.Minute).Milliseconds())
		if err := f.noItems("a run queued 29 minutes"); err != nil {
			return err
		}
		r.DB.ShiftClock((2 * time.Minute).Milliseconds())
		item, err := f.oneItem(ledger.CIItemQueueStalled)
		if err != nil {
			return err
		}
		if item.Watch.RunURL == "" || item.Watch.QueueMs < (30*time.Minute).Milliseconds() {
			return Violatedf("the stalled queue reads %+v", item.Watch)
		}
		r.DB.ShiftClock((6 * time.Hour).Milliseconds())
		if _, err := f.oneItem(ledger.CIItemQueueStalled); err != nil {
			return Violatedf("a run queued for hours, with the CI timeout long past: %v", err)
		}
		return nil
	})
}

func checkCITimeoutAndItsAnswers(r *Rig) error {
	if r.DB.ShiftClock == nil {
		return nil
	}
	f, err := ciRig(r, ledger.CIRunInProgress, nil)
	if err != nil {
		return err
	}
	attention := control.NewCIAttention(f.store)
	order := f.grant.WorkOrder
	r.DB.ShiftClock((3*time.Hour - time.Minute).Milliseconds())
	if err := f.noItems("a run a minute short of 3 hours"); err != nil {
		return err
	}
	r.DB.ShiftClock(2 * time.Minute.Milliseconds())
	if _, err := f.oneItem(ledger.CIItemNotFinished); err != nil {
		return err
	}
	if _, err := f.oneItem(ledger.CIItemNotFinished); err != nil {
		return Violatedf("a second read raised a different item: %v", err)
	}
	// A time under a second adds nothing, so it is refused and records nothing.
	if _, err := attention.Wait(r.Ctx, r.Scope, order, 500*time.Microsecond, r.Actor); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("Wait longer by 500 microseconds answered %v", err)
	}
	if count, _, err := r.eventsAbout(f.store, order, ledger.EventCIWaitExtended); err != nil || count != 0 {
		return Violatedf("a refused wait left %d events: %v", count, err)
	}
	return f.startsNothing(func() error {
		first, err := attention.Wait(r.Ctx, r.Scope, order, 0, r.Actor)
		if err != nil {
			return err
		}
		if first.ExtendedMs != (20*time.Minute).Milliseconds() || first.Kind != "" {
			return Violatedf("one Wait longer, which adds one p85 of 20 minutes, left %+v", first)
		}
		if err := f.noItems("a press of Wait longer"); err != nil {
			return err
		}
		r.DB.ShiftClock((20 * time.Minute).Milliseconds())
		if _, err := f.oneItem(ledger.CIItemNotFinished); err != nil {
			return Violatedf("the item did not return past the extended timeout: %v", err)
		}
		second, err := attention.Wait(r.Ctx, r.Scope, order, time.Hour, r.Actor)
		if err != nil {
			return err
		}
		if second.ExtendedMs != (80 * time.Minute).Milliseconds() {
			return Violatedf("a second press of an hour left %d ms extended, want 80 minutes", second.ExtendedMs)
		}
		if count, _, err := r.eventsAbout(f.store, order, ledger.EventCIWaitExtended); err != nil || count != 2 {
			return Violatedf("two presses left %d events: %v", count, err)
		}
		r.DB.ShiftClock((2 * time.Hour).Milliseconds())
		if _, err := f.oneItem(ledger.CIItemNotFinished); err != nil {
			return err
		}
		waiting, err := attention.Rerun(r.Ctx, r.Scope, order, r.Actor)
		if err != nil || waiting.Reruns != 1 {
			return Violatedf("a rerun counted %+v: %v", waiting, err)
		}
		if count, event, err := r.eventsAbout(f.store, order, ledger.EventCIRerunRequested); err != nil || count != 1 || event.Actor != r.Actor {
			return Violatedf("a rerun left %d events by %+v: %v", count, event.Actor, err)
		}
		// A rerun starts the head's CI clock again, and drops what Wait longer added before it.
		if err := f.noItems("a rerun"); err != nil {
			return err
		}
		r.DB.ShiftClock((3*time.Hour + time.Minute).Milliseconds())
		if item, err := f.oneItem(ledger.CIItemNotFinished); err != nil || item.Watch.ExtendedMs != 0 || item.Watch.Reruns != 1 {
			return Violatedf("a rerun that ran 3 hours reads %+v: %v", item.Watch, err)
		}
		return nil
	})
}

func checkCIEditsMoveTheItems(r *Rig) error {
	f, err := ciRig(r, ledger.CIRunQueued, nil)
	if err != nil {
		return err
	}
	before, err := f.ciWatch()
	if err != nil {
		return err
	}
	edit := func(key, value string) error {
		settings, err := ledger.DefaultCISettings().Set(key, value)
		if err != nil {
			return err
		}
		_, err = r.change(f.store, f.repository, control.RepositoryChange{CI: &settings}, r.Actor)
		return err
	}
	if err := edit("ci_queue_timeout_no_history", "2h"); err != nil {
		return err
	}
	after, err := f.ciWatch()
	if err != nil {
		return err
	}
	if after.QueueTimeoutAt-before.QueueTimeoutAt != (90 * time.Minute).Milliseconds() {
		return Violatedf("ci_queue_timeout_no_history from 30 minutes to 2 hours moved the queue limit from %d to %d", before.QueueTimeoutAt, after.QueueTimeoutAt)
	}
	// With five records the repository has history, which the multiplier then scales.
	if err := f.completeHeads(5, func() error { return nil }); err != nil {
		return err
	}
	if err := f.ciRead(olderHead, ciCheck(ledger.CIRunInProgress)); err != nil {
		return err
	}
	withHistory, err := f.ciWatch()
	if err != nil {
		return err
	}
	if !withHistory.History || withHistory.TimeoutMs != (12*time.Minute).Milliseconds() {
		return Violatedf("three times a p85 of four minutes reads %+v", withHistory)
	}
	if err := edit("ci_timeout_multiplier", "5"); err != nil {
		return err
	}
	moved, err := f.ciWatch()
	if err != nil {
		return err
	}
	if moved.TimeoutMs != (20*time.Minute).Milliseconds() || moved.TimeoutAt-withHistory.TimeoutAt != (8*time.Minute).Milliseconds() {
		return Violatedf("a multiplier of 5 moved the timeout from %+v to %+v", withHistory, moved)
	}
	return nil
}

func checkCIFindings(r *Rig) error {
	f, err := newForgeRig(r, ledger.ReviewOnPullRequest)
	if err != nil {
		return err
	}
	if err := f.report(f.opened(1, f.head)); err != nil {
		return err
	}
	attention := control.NewCIAttention(f.store)
	order := f.grant.WorkOrder
	if err := f.noItems("before any finding"); err != nil {
		return err
	}
	for _, kind := range ledger.CIItemKinds[2:] {
		finding := ledger.CIFinding{WorkOrder: order, Head: f.head, Kind: kind, Check: "unit", Conclusion: "timed_out", URL: "https://github.com/acme/widgets/actions/runs/9"}
		first, err := attention.Raise(r.Ctx, r.Scope, finding)
		if err != nil || !first {
			return Violatedf("%s raised %v: %v", kind, first, err)
		}
		if again, err := attention.Raise(r.Ctx, r.Scope, finding); err != nil || again {
			return Violatedf("%s raised twice: %v, %v", kind, again, err)
		}
	}
	items, err := f.ciItems()
	if err != nil || len(items) != len(ledger.CIItemKinds[2:]) {
		return Violatedf("each finding kind raises one item, and %v did: %v", kinds(items), err)
	}
	for _, item := range items {
		if item.Finding == nil || item.Finding.Check != "unit" || item.Since == 0 || item.Since != item.Finding.RaisedAt {
			return Violatedf("a finding's item reads %+v", item)
		}
	}
	if _, err := attention.Raise(r.Ctx, r.Scope, ledger.CIFinding{WorkOrder: order, Head: "old-head", Kind: ledger.CIItemBaseRed}); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a finding of a head that is not the pull request's head answered %v", err)
	}
	if _, err := attention.Raise(r.Ctx, r.Scope, ledger.CIFinding{WorkOrder: order, Head: f.head, Kind: ledger.CIItemNotFinished}); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a timeout raised as a finding answered %v", err)
	}
	delegate := ledger.Actor{Kind: ledger.ActorDelegate, ID: "delegate-1"}
	if _, err := attention.Rerun(r.Ctx, r.Scope, order, delegate); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a delegate's rerun answered %v", err)
	}
	if _, err := attention.Wait(r.Ctx, r.Scope, order, 0, delegate); !errors.Is(err, workorders.ErrForbidden) {
		return Violatedf("a delegate's Wait longer answered %v", err)
	}
	if _, err := attention.Wait(r.Ctx, r.Scope, order, 0, r.Actor); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("Wait longer with no timeout to extend answered %v", err)
	}
	// A finding that stands for a condition a later read can end leaves alone: the one kind goes, and the others stay.
	var cleared, again int64
	if err := f.store.Write(r.Ctx, r.Scope, "ci.finding_clear", func(tx ports.WriteTx) error {
		var err error
		if cleared, err = tx.CI().ClearCIFinding(order, f.head, ledger.CIItemBaseUnread); err != nil {
			return err
		}
		if again, err = tx.CI().ClearCIFinding(order, f.head, ledger.CIItemBaseUnread); err != nil {
			return err
		}
		_, err = tx.Append(ledger.EventDraft{Type: "note.added", Actor: r.Actor, Subject: ledger.Subject{Kind: "note", ID: "note"}})
		return err
	}); err != nil || cleared != 1 || again != 0 {
		return Violatedf("clearing one kind of finding removed %d and then %d: %v", cleared, again, err)
	}
	if left, err := f.ciItems(); err != nil || len(left) != len(items)-1 || slices.Contains(kinds(left), ledger.CIItemBaseUnread) {
		return Violatedf("after clearing one kind the items are %v: %v", kinds(left), err)
	}
	// An infrastructure failure offers a rerun, which counts, is recorded, and clears the head's findings.
	waiting, err := attention.Rerun(r.Ctx, r.Scope, order, r.Actor)
	if err != nil || waiting.Reruns != 1 {
		return Violatedf("a rerun of an infrastructure failure counted %+v: %v", waiting, err)
	}
	if err := f.noItems("a rerun"); err != nil {
		return err
	}
	if _, err := attention.Rerun(r.Ctx, r.Scope, order, r.Actor); !errors.Is(err, workorders.ErrInvalid) {
		return Violatedf("a rerun of a head that raises nothing answered %v", err)
	}
	return nil
}
