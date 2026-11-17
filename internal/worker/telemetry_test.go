package worker_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/worker"
	"ropeshift.com/ropeshift/internal/workorders"
)

func attribute(attributes []ports.Attribute, key string) any {
	for _, a := range attributes {
		if a.Key == key {
			return a.Value
		}
	}
	return nil
}

// TestALaneIsTaggedAtSpawn holds FR-OBS-2: the session's telemetry goes to the worker's receiver,
// tagged with what produced it.
func TestALaneIsTaggedAtSpawn(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.config.Telemetry = worker.LaneTelemetry{Endpoint: "http://127.0.0.1:7318", Header: "x-ropeshift-telemetry=t", Workspace: "default"}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	specs := r.driver.Specs()
	if len(specs) != 1 {
		t.Fatalf("%d sessions", len(specs))
	}
	got := specs[0].Telemetry
	want := lanes.TelemetryTags{
		Project: "default", Workspace: "default", WorkOrder: r.id, Lane: "lane-1", Role: lanes.RoleWriter, Generation: 1, Profile: "default",
		Repo: "repository", Branch: got.Tags.Branch, Worker: "test", Driver: "claude-code",
	}
	if got.Endpoint != "http://127.0.0.1:7318" || got.Header != "x-ropeshift-telemetry=t" || got.Tags != want || got.Tags.Branch == "" {
		t.Errorf("telemetry %+v, want tags %+v", got, want)
	}
	if got.Content || got.Traces {
		t.Errorf("a default profile asked for content or traces: %+v", got)
	}
}

func TestAWorkerWithNoReceiverTagsNothing(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if specs := r.driver.Specs(); len(specs) != 1 || specs[0].Telemetry.Endpoint != "" {
		t.Errorf("specs %+v", specs)
	}
}

func TestAProfilesTelemetryReachesTheSpec(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "[telemetry]\ntraces = true\nomit_repository = true\n")
	r.config.Telemetry = worker.LaneTelemetry{Endpoint: "http://127.0.0.1:7318", Header: "x=y"}
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := r.driver.Specs()[0].Telemetry
	if !got.Traces || got.Content || got.Tags.Repo != "" || got.Tags.Branch != "" {
		t.Errorf("telemetry %+v", got)
	}
}

func spanNamed(t *testing.T, telemetry *fake.Telemetry, name string) ports.Span {
	t.Helper()
	spans := telemetry.Spans(name)
	if len(spans) != 1 {
		t.Fatalf("%d spans named %s", len(spans), name)
	}
	return spans[0]
}

// TestSpansFollowALaneFromClaimToItsEnd reads a lane's whole life from the ledger and finds its claim,
// its lane, and its generation in the work order's trace, each with its parent.
func TestSpansFollowALaneFromClaimToItsEnd(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	telemetry := fake.NewTelemetry()
	spans := control.NewSpans(r.store, telemetry)
	if _, err := spans.Project(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	trace := ledger.WorkOrderTrace(r.id)
	claim := spanNamed(t, telemetry, ledger.SpanClaim)
	lane := spanNamed(t, telemetry, ledger.SpanLane)
	generation := spanNamed(t, telemetry, ledger.SpanGeneration)
	if claim.TraceID != trace || lane.TraceID != trace || generation.TraceID != trace {
		t.Errorf("traces %s %s %s, want %s", claim.TraceID, lane.TraceID, generation.TraceID, trace)
	}
	if claim.ParentID != ledger.WorkOrderSpan(r.id) || generation.ParentID != lane.SpanID || lane.SpanID != ledger.LaneSpan("lane-1") {
		t.Errorf("parents: claim %s, generation %s, lane %s", claim.ParentID, generation.ParentID, lane.SpanID)
	}
	if attribute(lane.Attributes, ledger.AttrOutcome) != "completed" || attribute(generation.Attributes, ledger.AttrStartReason) != "initial" ||
		lane.Start == 0 || lane.End < lane.Start {
		t.Errorf("lane %+v generation %+v", lane, generation)
	}
	if read, err := spans.Project(t.Context(), scope); err != nil || read != 0 {
		t.Errorf("a second pass read %d events: %v", read, err)
	}
}

func TestSpansStartAtTheHead(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	telemetry := fake.NewTelemetry()
	spans := control.NewSpans(r.store, telemetry)
	if err := spans.StartAtHead(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	if read, err := spans.Project(t.Context(), scope); err != nil || read != 0 || len(telemetry.Spans("")) != 0 {
		t.Errorf("read %d, spans %d, err %v", read, len(telemetry.Spans("")), err)
	}
}

// appendEvent writes one event straight to the log, as a projection would read it.
func (r *rig) appendEvent(t *testing.T, draft ledger.EventDraft) {
	t.Helper()
	err := r.store.Write(t.Context(), scope, "test.append", func(tx ports.WriteTx) error {
		_, err := tx.Append(draft)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func statusEvent(t *testing.T, status ledger.LaneStatus) ledger.EventDraft {
	t.Helper()
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.EventDraft{
		Type: ledger.EventLaneStatus, Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: "w"},
		Subject: ledger.Subject{Kind: ledger.SubjectLane, ID: "lane-9"}, Data: data,
	}
}

// TestStallTimeIsReadFromTheLanesStatus holds FR-OBS-1's stall time: how long a lane read `stalled`,
// from the first status that said so to the first that did not.
func TestStallTimeIsReadFromTheLanesStatus(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.appendEvent(t, statusEvent(t, ledger.LaneStatus{ActualState: "working", Liveness: "alive"}))
	r.appendEvent(t, statusEvent(t, ledger.LaneStatus{ActualState: "working", Liveness: "alive", Conditions: []string{string(lanes.Stalled)}}))
	r.shiftDatabase(t, 90_000)
	r.appendEvent(t, statusEvent(t, ledger.LaneStatus{ActualState: "working", Liveness: "unknown", Conditions: []string{string(lanes.Stalled)}}))
	r.shiftDatabase(t, 30_000)
	r.appendEvent(t, statusEvent(t, ledger.LaneStatus{ActualState: "tripped", Liveness: "unknown"}))
	telemetry := fake.NewTelemetry()
	if _, err := control.NewSpans(r.store, telemetry).Project(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	stalls := telemetry.Measurements(ledger.MetricStall)
	if len(stalls) != 1 || stalls[0].Value < 119.9 || stalls[0].Value > 121 || attribute(stalls[0].Attributes, lanes.AttrLane) != "lane-9" {
		t.Errorf("stalls %+v", stalls)
	}
	if got := telemetry.Measurements(ledger.MetricLivenessUnknown); len(got) != 1 {
		t.Errorf("unknown reads %+v", got)
	}
	if got := telemetry.Measurements(ledger.MetricBreakerTrips); len(got) != 1 {
		t.Errorf("trips %+v", got)
	}
}

func approvalEvent(t *testing.T, approval authority.Approval) ledger.EventDraft {
	t.Helper()
	data, err := json.Marshal(approval)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.EventDraft{
		Type: authority.EventApprovalChanged, Actor: ledger.Actor{Kind: ledger.ActorWorker, ID: "w"},
		Subject: ledger.Subject{Kind: authority.SubjectApproval, ID: approval.ID}, Data: data,
	}
}

// TestApprovalWaitIsTheTimeToADecision holds FR-OBS-1's approval wait, by who decided, with the
// pending count going up and back down. The summary a person reads stays out.
func TestApprovalWaitIsTheTimeToADecision(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	approval := authority.Approval{ID: "a-1", Lane: "lane-9", WorkOrder: "wo-9", Generation: 2, Kind: "tool", Summary: "rm -rf the secret plan", State: authority.ApprovalPending}
	r.appendEvent(t, approvalEvent(t, approval))
	r.shiftDatabase(t, 45_000)
	approval.State, approval.DecidedKind = authority.ApprovalApproved, string(ledger.ActorMember)
	r.appendEvent(t, approvalEvent(t, approval))
	telemetry := fake.NewTelemetry()
	if _, err := control.NewSpans(r.store, telemetry).Project(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	waits := telemetry.Measurements(ledger.MetricApprovalWait)
	if len(waits) != 1 || waits[0].Value < 44.9 || waits[0].Value > 46 || attribute(waits[0].Attributes, ledger.AttrApprovalDecidedBy) != "member" {
		t.Errorf("waits %+v", waits)
	}
	pending := 0.0
	for _, m := range telemetry.Measurements(ledger.MetricApprovalPending) {
		pending += m.Value
	}
	if len(telemetry.Measurements(ledger.MetricApprovalPending)) != 2 || pending != 0 {
		t.Errorf("pending %+v", telemetry.Measurements(ledger.MetricApprovalPending))
	}
	span := spanNamed(t, telemetry, ledger.SpanApprovalWait)
	if span.TraceID != ledger.WorkOrderTrace("wo-9") || span.ParentID != ledger.GenerationSpan("lane-9", 2) {
		t.Errorf("span %+v", span)
	}
	for _, a := range span.Attributes {
		if text, ok := a.Value.(string); ok && strings.Contains(text, "secret") {
			t.Errorf("the span carries the summary: %+v", a)
		}
	}
}

func messageEvent(t *testing.T, message messages.Message) ledger.EventDraft {
	t.Helper()
	draft, err := messages.RecordEvent(message, ledger.Actor{Kind: ledger.ActorMember, ID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	return draft
}

// TestMessageLatencyRunsFromHandOffToReply holds FR-OBS-1's message latency, and keeps a body out of
// every span and measurement.
func TestMessageLatencyRunsFromHandOffToReply(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	message := messages.Message{ID: "msg-1", To: messages.Target{Lane: "lane-9", Generation: 1}, Kind: messages.KindInstruction, Body: "the secret plan", State: messages.Queued}
	for _, step := range []struct {
		state messages.State
		shift int64
	}{{messages.Queued, 0}, {messages.HandedOff, 1000}, {messages.Admitted, 500}, {messages.Replied, 6500}} {
		r.shiftDatabase(t, step.shift)
		message.State = step.state
		r.appendEvent(t, messageEvent(t, message))
	}
	telemetry := fake.NewTelemetry()
	if _, err := control.NewSpans(r.store, telemetry).Project(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	latency := telemetry.Measurements(ledger.MetricReplyLatency)
	if len(latency) != 1 || latency[0].Value < 6.9 || latency[0].Value > 7.5 {
		t.Errorf("latency %+v", latency)
	}
	send, inject := spanNamed(t, telemetry, ledger.SpanMessageSend), spanNamed(t, telemetry, ledger.SpanMessageInject)
	if send.Kind != ports.SpanProducer || inject.Kind != ports.SpanConsumer || len(inject.Links) != 1 || inject.Links[0].SpanID != send.SpanID {
		t.Errorf("send %+v inject %+v", send, inject)
	}
	if states := telemetry.Measurements(ledger.MetricMessageState); len(states) != 1 || attribute(states[0].Attributes, ledger.AttrMessageState) != "replied" {
		t.Errorf("states %+v", states)
	}
	for _, span := range telemetry.Spans("") {
		for _, a := range span.Attributes {
			if text, ok := a.Value.(string); ok && strings.Contains(text, "secret") {
				t.Errorf("span %s carries the body", span.Name)
			}
		}
	}
}

// TestPerLaneMetricsFromDriverReports holds part of FR-OBS-1: context, tokens, and cost per lane,
// with a turn's cost the difference between running totals.
func TestPerLaneMetricsFromDriverReports(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{
		{Bound: true},
		{Context: &lanes.Usage{InputTokens: 10, CacheReadTokens: 4990}},
		{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.25, Usage: lanes.Usage{InputTokens: 10, OutputTokens: 30, CacheReadTokens: 4990}}},
		{Context: &lanes.Usage{InputTokens: 20, CacheReadTokens: 6000}},
		r.reports(workorders.OutcomeCompleted),
		{Turn: &fake.DriverTurn{Status: ports.TurnCompleted, TotalUSD: 0.4, Usage: lanes.Usage{InputTokens: 20, OutputTokens: 5}}},
	}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	contexts := r.telemetry.Measurements(ledger.MetricContextTokens)
	if len(contexts) != 2 || contexts[0].Value != 5000 || contexts[1].Value != 6020 || contexts[0].Kind != ports.MetricGauge {
		t.Errorf("context %+v", contexts)
	}
	baselines := r.telemetry.Measurements(ledger.MetricContextBaseline)
	if len(baselines) != 1 || baselines[0].Value != 5000 {
		t.Errorf("baseline %+v", baselines)
	}
	costs := r.telemetry.Measurements(ledger.MetricCost)
	if len(costs) != 2 || math.Abs(costs[0].Value-0.25) > 1e-9 || math.Abs(costs[1].Value-0.15) > 1e-9 {
		t.Errorf("cost %+v", costs)
	}
	tokens := map[string]float64{}
	for _, m := range r.telemetry.Measurements(ledger.MetricTokens) {
		tokens[attribute(m.Attributes, ledger.AttrTokenType).(string)] += m.Value
	}
	if tokens["input"] != 30 || tokens["output"] != 35 || tokens["cache_read"] != 4990 || tokens["cache_write"] != 0 {
		t.Errorf("tokens %v", tokens)
	}
	for _, m := range append(contexts, costs...) {
		if attribute(m.Attributes, lanes.AttrLane) != "lane-1" || attribute(m.Attributes, lanes.AttrWorkOrder) != r.id ||
			attribute(m.Attributes, lanes.AttrGeneration) != int64(1) {
			t.Errorf("measurement %s is not the lane's: %+v", m.Name, m.Attributes)
		}
	}
}

// laneResult is what the ledger recorded at the lane's end: the payload of the change it made to its work order, or,
// for a lane that moved its work order nowhere, as a canceled lane does, its own end.
func (r *laneRig) laneResult() workorders.LaneResult {
	r.t.Helper()
	raw := r.outcome().Payload
	if len(raw) == 0 {
		err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
			events, err := tx.EventsAbout(ledger.Subject{Kind: ledger.SubjectLane, ID: "lane-1"})
			for _, e := range events {
				if e.Type == ledger.EventLaneEnded {
					raw = e.Data
				}
			}
			return err
		})
		if err != nil {
			r.t.Fatal(err)
		}
	}
	var result workorders.LaneResult
	if err := json.Unmarshal(raw, &result); err != nil {
		r.t.Fatalf("the ledger holds no result of the lane: %v", err)
	}
	return result
}

// TestATurnWhoseCostWentUnreportedIsCounted holds the lower bound of a lane's cost: a turn that stopped reports no
// cost, and a session that crashed never reports its last turn's, so the worker counts each, under the lane and the
// generation it ran in. The ledger's record of the lane's end counts the same turns, so the measures and the delivery
// record read a lower bound where the lane table does. A lane whose every turn reported its cost counts none.
func TestATurnWhoseCostWentUnreportedIsCounted(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		restarts string
		steps    func(r *laneRig) []fake.DriverStep
		want     int
	}{
		{"a turn that stopped", "", func(r *laneRig) []fake.DriverStep {
			return []fake.DriverStep{
				{Bound: true}, {Turn: &fake.DriverTurn{Status: ports.TurnStopped, Reason: "aborted_streaming"}},
				r.reports(workorders.OutcomeCompleted), completed(0.25),
			}
		}, 1},
		{"a session that died with no result for its input", noRestarts, func(*laneRig) []fake.DriverStep {
			return []fake.DriverStep{{Bound: true}, {Crash: true}}
		}, 1},
		{"a session that crashed at rest", noRestarts, func(*laneRig) []fake.DriverStep {
			return []fake.DriverStep{{Bound: true}, completed(0.25), {Crash: true}}
		}, 1},
		{"a stream that closed with no end to its session", noRestarts, func(*laneRig) []fake.DriverStep {
			return []fake.DriverStep{{Bound: true}, {Vanish: true}}
		}, 1},
		{"turns that reported their cost", "", func(r *laneRig) []fake.DriverStep {
			return []fake.DriverStep{{Bound: true}, completed(0.25), r.reports(workorders.OutcomeCompleted), completed(0.4)}
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := laneRigWith(t, tt.restarts)
			r.driver.Play(script(tt.steps(r)))
			if err := r.subject.Converge(t.Context()); err != nil {
				t.Fatal(err)
			}
			unreported := r.telemetry.Measurements(ledger.MetricCostUnreported)
			if len(unreported) != tt.want {
				t.Fatalf("%d turns counted unreported, want %d: %+v", len(unreported), tt.want, unreported)
			}
			for _, m := range unreported {
				if m.Kind != ports.MetricCounter || m.Unit != "{turn}" || m.Value != 1 {
					t.Errorf("an unreported turn is measured as %+v", m)
				}
				if attribute(m.Attributes, lanes.AttrLane) != "lane-1" || attribute(m.Attributes, lanes.AttrWorkOrder) != r.id ||
					attribute(m.Attributes, lanes.AttrGeneration) != int64(1) {
					t.Errorf("an unreported turn is not the lane's: %+v", m.Attributes)
				}
			}
			if got := r.laneResult().UnknownCostTurns; got != int64(tt.want) {
				t.Errorf("the ledger counts %d turns of unknown cost, and the lane table %d", got, tt.want)
			}
		})
	}
}

// A crashed lane starts again, and the turn the crash left unreported stays in the ledger's record of the lane's end,
// which sums every generation's cost.
func TestATurnACrashLeftUnreportedOutlivesItsGeneration(t *testing.T) {
	r := laneRigWith(t, restarts)
	r.driver.Play(script(crashed()))
	r.driver.PlayAfter(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.01)}))
	r.start()
	r.laneState(lanes.Backoff)
	r.clock.Advance(31 * time.Second)
	r.subject.Wait()
	if specs := r.driver.Specs(); len(specs) != 2 {
		t.Fatalf("%d sessions, want the crashed one and the one after it", len(specs))
	}
	if got := r.laneResult().UnknownCostTurns; got != 1 {
		t.Errorf("the ledger counts %d turns of unknown cost over both generations, want 1", got)
	}
}

// A stop whose interrupt never lands ends in a kill. Nothing crashed, but the turn it killed never got the result
// that carries its cost, so it counts as unreported all the same.
func TestATurnAStopKilledIsCountedUnreported(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, "")
	working := make(chan struct{})
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, {Do: func(ports.SessionSpec) { close(working) }}, {Stuck: true}}))
	r.start()
	<-working
	r.cancelWork(t)
	if err := r.subject.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.subject.Reconcile(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitEnded(t)
	if unreported := r.telemetry.Measurements(ledger.MetricCostUnreported); len(unreported) != 1 {
		t.Fatalf("%d turns counted unreported, want 1: %+v", len(unreported), unreported)
	}
	if got := r.laneResult().UnknownCostTurns; got != 1 {
		t.Errorf("the ledger counts %d turns of unknown cost, and the lane table 1", got)
	}
}

// A worker that finds a lane's session gone, with no end it can read, cannot see whether the session ended inside a
// turn, which then reported no cost. What it last recorded of the lane decides: a lane at rest ran no turn, unless an
// input a crash left unknown reached the session, a session that never started ran none, and a lane at work counts
// its turn, in its telemetry and in the ledger's record of its end alike.
func TestATurnASessionNobodySawEndMayHaveEndedInIsCounted(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		actual lanes.ActualState
		spawn  lanes.EffectState
		input  bool
		want   int
	}{
		{"a lane at work", lanes.Working, lanes.EffectCompleted, false, 1},
		{"a lane at rest", lanes.Idle, lanes.EffectCompleted, false, 0},
		{"a lane at rest with an input a crash left unknown", lanes.Idle, lanes.EffectCompleted, true, 1},
		{"a lane whose session never started", lanes.Starting, lanes.EffectFailed, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newLaneRig(t)
			ctx := t.Context()
			record := lanes.Record{
				ID: "lane-1", WorkOrder: r.id, ClaimID: r.grant.ClaimID, Fence: r.grant.Fence, Generation: 1, State: lanes.StateRunning,
				TokenDigest: lanes.TokenDigest("token"), Process: 1,
			}
			begun := ledger.UploadEvent{
				Epoch: r.subject.Session().Epoch, Type: ledger.UploadLaneStarted, WorkOrder: r.id, ClaimID: record.ClaimID,
				Fence: record.Fence, Lane: "lane-1", Generation: 1, OccurredAt: 1,
			}
			if _, err := r.journal.BeginLane(ctx, record, begun); err != nil {
				t.Fatal(err)
			}
			if err := r.journal.SaveActual(ctx, "lane-1", tt.actual, ""); err != nil {
				t.Fatal(err)
			}
			spawn := lanes.NewEffect(lanes.EffectSession, "lane-1", 1, "", "")
			if _, err := r.journal.BeginEffect(ctx, spawn); err != nil {
				t.Fatal(err)
			}
			if err := r.journal.FinishEffect(ctx, spawn.Key, tt.spawn, ""); err != nil {
				t.Fatal(err)
			}
			if tt.input {
				if _, err := r.journal.BeginEffect(ctx, lanes.NewEffect(lanes.EffectInput, "lane-1", 1, "", "nudge")); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.subject.Converge(ctx); err != nil {
				t.Fatal(err)
			}
			if change := r.outcome(); change.Reason != workorders.ReasonWorkerRestarted {
				t.Fatalf("outcome %+v", change)
			}
			unreported := r.telemetry.Measurements(ledger.MetricCostUnreported)
			if len(unreported) != tt.want {
				t.Fatalf("%d turns counted unreported, want %d: %+v", len(unreported), tt.want, unreported)
			}
			for _, m := range unreported {
				if m.Kind != ports.MetricCounter || m.Unit != "{turn}" || m.Value != 1 || attribute(m.Attributes, lanes.AttrLane) != "lane-1" ||
					attribute(m.Attributes, lanes.AttrWorkOrder) != r.id || attribute(m.Attributes, lanes.AttrGeneration) != int64(1) {
					t.Errorf("an unreported turn is measured as %+v", m)
				}
			}
			if got := r.laneResult().UnknownCostTurns; got != int64(tt.want) {
				t.Errorf("the ledger counts %d turns of unknown cost, want %d", got, tt.want)
			}
		})
	}
}

// A worker that read a session's end counted the turn it crashed in, and held that count only in memory until the
// lane's next generation or its end. When that worker dies first, and the next one finds the session gone with no end
// it can read, the lane's end still counts the turn: once in the ledger, and not again in the telemetry, which the
// first worker wrote as it read the end.
func TestATurnACrashLeftUnreportedOutlivesTheWorkerThatReadIt(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, restarts)
	r.driver.Play(script(crashed()))
	r.start()
	r.laneState(lanes.Backoff)
	r.awaitCharge(t)
	r.stopWorker()
	r.subject.Wait()
	driver := fake.NewAgentDriver()
	driver.Leave("lane-1", 1, sessionDead)
	config := *r.config
	config.Driver = driver
	next := worker.New(r.link, r.journal, r.clock, worker.Config{Name: "test", Lanes: &config, Telemetry: r.telemetry})
	if err := next.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := next.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if change := r.outcome(); change.Reason != workorders.ReasonWorkerRestarted {
		t.Fatalf("outcome %+v", change)
	}
	if unreported := r.telemetry.Measurements(ledger.MetricCostUnreported); len(unreported) != 1 {
		t.Errorf("%d turns counted unreported, want the one the crash left: %+v", len(unreported), unreported)
	}
	if got := r.laneResult().UnknownCostTurns; got != 1 {
		t.Errorf("the ledger counts %d turns of unknown cost, want the one the crash left", got)
	}
}
