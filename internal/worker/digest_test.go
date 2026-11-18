package worker_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

func ms(n int64) *int64 { return &n }

// digestOf reads what the ledger holds of one generation of lane-1's digest.
func (r *laneRig) digestOf(t *testing.T, generation int64) (lanes.Digest, error) {
	t.Helper()
	var kept ledger.GenerationDigest
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		kept, err = tx.Lanes().Digest(ledger.DigestOwnerLane, "lane-1", generation)
		return err
	})
	if err != nil {
		return lanes.Digest{}, err
	}
	var digest lanes.Digest
	if err := json.Unmarshal(kept.Body, &digest); err != nil {
		t.Fatal(err)
	}
	return digest, nil
}

func (r *laneRig) digestedEvents(t *testing.T) int {
	t.Helper()
	count := 0
	err := r.store.Read(t.Context(), scope, func(tx ports.ReadTx) error {
		events, err := control.ReadAllEvents(tx.Events)
		for _, event := range events {
			if event.Type == ledger.EventGenerationDigested && event.Subject.ID == "lane-1" {
				count++
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// TestAHeadlessGenerationRecordsItsDigestWithItsEnd is FR-SLOG-1 and FR-SLOG-2 for a headless lane: the worker folds
// the events it already receives, and the ledger holds the digest once the generation has ended.
func TestAHeadlessGenerationRecordsItsDigestWithItsEnd(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true},
		{Tools: []lanes.ToolUse{{Name: "Edit", Path: "/repository-lanes/lane-1/internal/a.go"}, {Name: "Bash"}}},
		r.reports(workorders.OutcomeCompleted),
		{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.25, DurationMs: ms(700), APIMs: ms(400)}},
	}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	digest, err := r.digestOf(t, 1)
	if err != nil {
		t.Fatalf("the ledger holds no digest: %v", err)
	}
	if digest.Source != lanes.DigestStream || digest.Turns != 1 || digest.ToolCalls["Edit"] != 1 || digest.ToolCalls["Bash"] != 1 ||
		!slices.Equal(digest.FilesWritten, []string{"internal/a.go"}) || digest.TurnMs == nil || *digest.TurnMs != 700 ||
		digest.APIMs == nil || *digest.APIMs != 400 || !digest.Complete || digest.StartedAt == 0 || digest.EndedAt < digest.StartedAt {
		t.Errorf("digest %+v", digest)
	}
	if got := r.digestedEvents(t); got != 1 {
		t.Errorf("%d events say a digest was kept", got)
	}
	if left, err := r.journal.UnrecordedDigests(t.Context()); err != nil || len(left) != 0 {
		t.Errorf("the journal still holds %+v, %v", left, err)
	}
}

// TestAnInteractiveGenerationsDigestComesFromItsHooks builds from the hooks and from nothing the driver says:
// the pane driver reports turns and tools of its own, and counting both would count each twice.
func TestAnInteractiveGenerationsDigestComesFromItsHooks(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "run_mode = \"tmux\"\n")
	g := newGate()
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true}, {Tools: []lanes.ToolUse{{Name: "Grep"}}}, g.step(), r.reports(workorders.OutcomeCompleted), completed(0.01),
	}))
	r.atGate(g)
	r.hook(t, lanes.HookEvent{Event: lanes.EventUserPromptSubmit, Subject: "s", AtMs: 1000})
	r.hook(t, lanes.HookEvent{Event: lanes.EventPreToolUse, Subject: "t-1", Tool: "Edit", Path: "/repository-lanes/lane-1/docs/a.md", AtMs: 1100})
	r.hook(t, lanes.HookEvent{Event: lanes.EventPreToolUse, Subject: "t-2", Tool: "Bash", AtMs: 1200})
	r.hook(t, lanes.HookEvent{Event: lanes.EventStop, Subject: "s", AtMs: 1600})
	g.free()
	r.waitForTheLane(t)
	if err := r.subject.Upload(t.Context()); err != nil {
		t.Fatal(err)
	}
	digest, err := r.digestOf(t, 1)
	if err != nil {
		t.Fatalf("the ledger holds no digest: %v", err)
	}
	if digest.Source != lanes.DigestHooks || digest.Turns != 1 || digest.ToolCalls["Edit"] != 1 || digest.ToolCalls["Bash"] != 1 ||
		digest.ToolCalls["Grep"] != 0 || !slices.Equal(digest.FilesWritten, []string{"docs/a.md"}) ||
		digest.TurnMs == nil || *digest.TurnMs != 600 || digest.APIMs != nil || !digest.Complete {
		t.Errorf("digest %+v", digest)
	}
}

// TestAClearEndsOneGenerationsDigestAndBeginsTheNext holds that a digest is a generation's own: what a session
// did before a clear is the first generation's, and the clear's events are the second's.
func TestAClearEndsOneGenerationsDigestAndBeginsTheNext(t *testing.T) {
	t.Parallel()
	g := newGate()
	r := laneRigWith(t, "")
	r.finishLater(g)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Tools: []lanes.ToolUse{{Name: "Edit", Path: "a.go"}}}, g.step(), {Tools: []lanes.ToolUse{{Name: "Write", Path: "b.go"}}}, {Hold: true}}))
	r.start()
	select {
	case <-g.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the session never reached its gate")
	}
	r.eventually("a turn in progress", func(view ledger.LaneView) bool { return view.ActualState == string(lanes.Working) })
	// The scripted session is at its gate, and the worker may not have read the Edit before it yet. The clear
	// comes from a hook, which no stream orders against, so the generation ends at whatever the worker has read
	// when the clear arrives. The test clears once the worker has read what the first generation did.
	r.eventually("the first generation's Edit read", func(ledger.LaneView) bool {
		digest, ok := r.subject.DigestSoFar("lane-1")
		return ok && digest.ToolCalls["Edit"] == 1
	})
	r.cleared(t, clearedSession)
	r.read()
	first, err := r.digestOf(t, 1)
	if err != nil {
		t.Fatalf("the generation a clear ended has no digest: %v", err)
	}
	if first.ToolCalls["Edit"] != 1 || first.ToolCalls["Write"] != 0 || first.EndedAt == 0 {
		t.Errorf("the first generation's digest %+v", first)
	}
	g.free()
	r.eventually("the second generation's tool", func(ledger.LaneView) bool {
		r.driver.StopEverySession()
		_, err := r.digestOf(t, 2)
		return err == nil
	})
	second, err := r.digestOf(t, 2)
	if err != nil {
		t.Fatal(err)
	}
	if second.ToolCalls["Write"] != 1 || second.ToolCalls["Edit"] != 0 {
		t.Errorf("the second generation's digest %+v", second)
	}
}

// TestARestartRecordsTheDigestACrashLeft is FR-SLOG-2's crash: a generation that ended, whose digest was never
// recorded, is recorded once by the next start, and a second start adds nothing.
func TestARestartRecordsTheDigestACrashLeft(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	record := lanes.Record{ID: "lane-1", WorkOrder: r.id, ClaimID: r.grant.ClaimID, Fence: r.grant.Fence, Generation: 1, State: lanes.StateRunning}
	begun := ledger.UploadEvent{
		Epoch: r.subject.Session().Epoch, Type: ledger.UploadLaneStarted, WorkOrder: r.id, ClaimID: r.grant.ClaimID, Fence: r.grant.Fence,
		Lane: "lane-1", Generation: 1, OccurredAt: 1,
	}
	if _, err := r.journal.BeginLane(t.Context(), record, begun); err != nil {
		t.Fatal(err)
	}
	// The session was started, so the restart accounts for the lane and starts nothing.
	spawn := lanes.NewEffect(lanes.EffectSession, "lane-1", 1, "", "")
	if _, err := r.journal.BeginEffect(t.Context(), spawn); err != nil {
		t.Fatal(err)
	}
	if err := r.journal.FinishEffect(t.Context(), spawn.Key, lanes.EffectCompleted, ""); err != nil {
		t.Fatal(err)
	}
	builder := lanes.NewDigest(lanes.DigestStream, "/repository-lanes/lane-1", 100)
	builder.Calls(3, []lanes.ToolUse{{Name: "Edit", Path: "a.go"}})
	builder.Turn(4, lanes.TurnFacts{})
	builder.Finish(900)
	state, err := builder.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := r.journal.SaveDigest(t.Context(), "lane-1", 1, state, true); err != nil {
		t.Fatal(err)
	}
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	digest, err := r.digestOf(t, 1)
	if err != nil {
		t.Fatalf("the restart recorded nothing: %v", err)
	}
	if digest.Turns != 1 || digest.ToolCalls["Edit"] != 1 || digest.EndedAt != 900 {
		t.Errorf("digest %+v", digest)
	}
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := r.digestedEvents(t); got != 1 {
		t.Errorf("%d events say a digest was kept", got)
	}
	if _, err := r.journal.Digest(t.Context(), "lane-1", 1); err != nil {
		t.Error(err)
	}
	if _, err := r.digestOf(t, 2); !errors.Is(err, ports.ErrNotFound) {
		t.Errorf("a generation that never ran has a digest: %v", err)
	}
}
