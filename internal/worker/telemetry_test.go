package worker_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

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
		Workspace: "default", WorkOrder: r.id, Lane: "lane-1", Role: lanes.RoleWriter, Generation: 1, Profile: "default",
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
