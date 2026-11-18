package ledger

import "testing"

const hour = int64(60 * 60 * 1000)

func limited(resetsAt int64) *SignInUsage {
	return &SignInUsage{Limited: true, Window: UsageWindowSession, ResetsAtMs: resetsAt, Source: UsageFromSession}
}

// A scheduled start waits while its reset is ahead, and once it came, waits for the worker's first word since. It
// starts the orchestrator when that word says the limit is gone, does nothing for an orchestrator that already runs,
// and when the limit still holds, moves to the reset reported since or waits for the next one, counting the miss.
func TestAScheduledStartsStep(t *testing.T) {
	start := ScheduledStart{Worker: "w", StartsAt: 10 * hour}
	free := &SignInUsage{}
	cases := []struct {
		name     string
		usage    *SignInUsage
		reported int64
		runs     bool
		now      int64
		step     StartStep
		next     ScheduledStart
	}{
		{"its reset is ahead", limited(10 * hour), 9 * hour, false, 9 * hour, StartWaits, start},
		{"the worker said nothing since its reset", limited(10 * hour), 9 * hour, false, 10*hour + 1, StartWaits, start},
		{"the limit is gone at its reset", free, 10*hour + 1, false, 10*hour + 2, StartFires, start},
		{"the limit ended before its reset", free, 9 * hour, false, 9*hour + 1, StartFires, start},
		{"the worker reports no usage", nil, 0, false, 10 * hour, StartFires, start},
		{"the orchestrator already runs", free, 10*hour + 1, true, 10*hour + 2, StartEnds, start},
		{"the limit holds past its reset, with no reset reported", limited(0), 10*hour + 1, false, 10*hour + 2, StartMoves, ScheduledStart{Worker: "w", Missed: 1}},
		{"the limit holds past its reset, with a later reset reported", limited(15 * hour), 10*hour + 1, false, 10*hour + 2, StartMoves, ScheduledStart{Worker: "w", StartsAt: 15 * hour, Missed: 1}},
		{"an earlier reset was reported", limited(8 * hour), 7 * hour, false, 7 * hour, StartMoves, ScheduledStart{Worker: "w", StartsAt: 8 * hour}},
	}
	for _, c := range cases {
		step, next := start.Step(c.usage, c.reported, c.runs, c.now)
		if step != c.step || next != c.next {
			t.Errorf("%s: %s %+v, want %s %+v", c.name, step, next, c.step, c.next)
		}
	}
	waiting := ScheduledStart{Worker: "w", Missed: 1}
	if step, next := waiting.Step(limited(20*hour), 12*hour, false, 12*hour); step != StartMoves || next.StartsAt != 20*hour || next.Missed != 1 {
		t.Errorf("a start that waits for the next reset, when one is reported: %s %+v", step, next)
	}
	if step, _ := waiting.Step(limited(0), 12*hour, false, 12*hour); step != StartWaits {
		t.Errorf("a start that waits for the next reset, when none is reported: %s", step)
	}
}

// What holds an orchestrator is named most basic first: a worker that is not connected, a breaker that tripped, then a
// sign-in at its limit, with the window, its reset, and whether that reset is too old to trust.
func TestWhatHoldsAnOrchestrator(t *testing.T) {
	tripped := &Orchestrator{Desired: OrchestratorRunning, Actual: "tripped"}
	if hold := OrchestratorHoldOf(false, tripped, limited(10*hour), 0, 9*hour); hold == nil || hold.Reason != HoldOffline {
		t.Errorf("an offline worker: %+v", hold)
	}
	if hold := OrchestratorHoldOf(true, tripped, limited(10*hour), 0, 9*hour); hold == nil || hold.Reason != HoldTripped {
		t.Errorf("a tripped breaker: %+v", hold)
	}
	hold := OrchestratorHoldOf(true, nil, limited(10*hour), 8*hour, 9*hour)
	if hold == nil || hold.Reason != HoldUsageLimit || hold.Window != UsageWindowSession || hold.ResetsAt != 10*hour || hold.ReadAt != 8*hour || hold.Stale {
		t.Errorf("a sign-in at its limit: %+v", hold)
	}
	if hold := OrchestratorHoldOf(true, nil, limited(10*hour), 8*hour, 10*hour+1); hold == nil || !hold.Stale {
		t.Errorf("a reset that passed with the limit holding: %+v", hold)
	}
	weekly := &SignInUsage{Limited: true, Window: UsageWindowWeekly, ResetsAtMs: 200 * hour, Source: UsageFromSession}
	if hold := OrchestratorHoldOf(true, nil, weekly, 0, 170*hour); hold == nil || !hold.Stale {
		t.Errorf("a weekly reset read longer ago than a week: %+v", hold)
	}
	if hold := OrchestratorHoldOf(true, nil, weekly, 3*hour, 170*hour); hold == nil || hold.Stale {
		t.Errorf("a weekly reset read within the week: %+v", hold)
	}
	if hold := OrchestratorHoldOf(true, &Orchestrator{Desired: OrchestratorRunning, Actual: "working"}, &SignInUsage{}, 0, 9*hour); hold != nil {
		t.Errorf("nothing holds a running orchestrator: %+v", hold)
	}
}
