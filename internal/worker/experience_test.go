package worker_test

import (
	"math"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/authority"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/workorders"
)

func (r *laneRig) experience(subject string) measures.Experience {
	r.t.Helper()
	var record measures.Experience
	err := r.store.Read(r.t.Context(), scope, func(tx ports.ReadTx) error {
		var err error
		record, err = tx.Measures().Experience(measures.SubjectLane, subject)
		return err
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return record
}

// TestALanesExperienceHoldsItsSettingsAndItsOutcome holds FR-EXP-1 and FR-EXP-2 for a lane: its record
// opens when its session binds, with the agent, both models, the effort, its role's thresholds, and
// its work's size, and its end fills the outcome once, with the cost its agent reported.
func TestALanesExperienceHoldsItsSettingsAndItsOutcome(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	record := r.experience("lane-1")
	want := measures.Settings{
		Driver: "claude-code", ModelRequested: "sonnet", ModelResolved: "resolved-model", Effort: "medium", RunMode: ports.RunModeHeadless,
		ProfileDigest: record.Settings.ProfileDigest, WorkOrderSize: measures.SizeUnknown,
		Context: measures.ContextSettings{Role: lanes.RoleWriter, WarnTokens: 120000, CompactTokens: 150000, HandoffTokens: 180000},
	}
	if record.Settings != want || record.Settings.ProfileDigest == "" || record.Chosen != string(workorders.KindChange) {
		t.Errorf("record %+v\nwant settings %+v", record, want)
	}
	if record.Outcome == nil || record.Outcome.Status != string(workorders.OutcomeCompleted) || math.Abs(record.Outcome.CostUSD-0.25) > 1e-9 ||
		record.OutcomeObservedAt == 0 {
		t.Errorf("outcome %+v", record.Outcome)
	}
}

// TestMeasuresExportAsMetrics copies each window's measures out as `ropeshift.measure.*` gauges, which a
// backend charts; the measures themselves need no pipeline.
func TestMeasuresExportAsMetrics(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewAcceptances(r.store).Accept(t.Context(), scope, r.id, member); err != nil {
		t.Fatal(err)
	}
	telemetry := fake.NewTelemetry()
	if err := control.NewMeasures(r.store, telemetry).Export(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	windows := map[string]float64{}
	for _, m := range telemetry.Measurements(control.MetricMeasurePrefix + "completed") {
		windows[attribute(m.Attributes, control.AttrMeasureWindow).(string)] = m.Value
		if m.Kind != ports.MetricGauge {
			t.Errorf("measure %+v is no gauge", m)
		}
	}
	if len(windows) != len(control.MeasureWindows) || windows["7d"] != 1 {
		t.Errorf("completed by window %v", windows)
	}
	cost := telemetry.Measurements(control.MetricMeasurePrefix + "cost_per_outcome")
	if len(cost) == 0 || math.Abs(cost[0].Value-0.25) > 1e-9 || attribute(cost[0].Attributes, "ropeshift.measure.kind") != "change" {
		t.Errorf("cost per outcome %+v", cost)
	}
	if err := control.NewMeasures(r.store, nil).Export(t.Context(), scope); err != nil {
		t.Errorf("an export with no telemetry: %v", err)
	}
}

// TestDeliveryMeasuresExportAsMetrics copies delivery look back out as `ropeshift.measure.delivery.*` gauges
// by window, labeled only by window, quantile, kind, size, capability class, repository, and segment kind,
// never by a work order or a person (FR-OBS-15).
func TestDeliveryMeasuresExportAsMetrics(t *testing.T) {
	t.Parallel()
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewAcceptances(r.store).Accept(t.Context(), scope, r.id, member); err != nil {
		t.Fatal(err)
	}
	if _, err := control.NewDeliveryFold(r.store).Fold(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	telemetry := fake.NewTelemetry()
	if err := control.NewMeasures(r.store, telemetry).Export(t.Context(), scope); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{control.AttrMeasureWindow: true}
	for _, label := range []string{"quantile", "kind", "size", "capability_class", "repository", "segment_kind"} {
		allowed["ropeshift.measure."+label] = true
	}
	for _, name := range []string{"delivery.elapsed", "delivery.time", "delivery.throughput", "delivery.segment_share"} {
		measured := telemetry.Measurements(control.MetricMeasurePrefix + name)
		if len(measured) == 0 {
			t.Errorf("no %s", name)
		}
		for _, m := range measured {
			for _, a := range m.Attributes {
				if !allowed[a.Key] {
					t.Errorf("%s carries %s", name, a.Key)
				}
			}
		}
	}
	quantiles := map[string]bool{}
	for _, m := range telemetry.Measurements(control.MetricMeasurePrefix + "delivery.elapsed") {
		if attribute(m.Attributes, control.AttrMeasureWindow) == "7d" && attribute(m.Attributes, "ropeshift.measure.kind") == "change" {
			quantiles[attribute(m.Attributes, "ropeshift.measure.quantile").(string)] = true
			if m.Unit != "ms" || m.Kind != ports.MetricGauge {
				t.Errorf("elapsed %+v", m)
			}
		}
	}
	if !quantiles["p50"] || !quantiles["p85"] {
		t.Errorf("elapsed quantiles %v", quantiles)
	}
}

// TestOnlyAPersonsApprovalInterrupts reads approvals from the log: one a member decided reached a
// person, and one an orchestrator decided under delegation reached nobody.
func TestOnlyAPersonsApprovalInterrupts(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	for _, decided := range []struct {
		id   string
		kind ledger.ActorKind
	}{{"approval-person", ledger.ActorMember}, {"approval-delegated", ledger.ActorOrchestrator}} {
		approval := authority.Approval{ID: decided.id, Lane: "lane-9", WorkOrder: "wo-9", Generation: 1, Kind: "tool", State: authority.ApprovalPending}
		r.appendEvent(t, approvalEvent(t, approval))
		approval.State, approval.DecidedKind = authority.ApprovalApproved, string(decided.kind)
		r.appendEvent(t, approvalEvent(t, approval))
	}
	report, err := control.NewMeasures(r.store, nil).Report(t.Context(), scope, "7d", 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if report.Interruptions != 1 || report.InterruptionsByKind[measures.InterruptionApproval] != 1 {
		t.Errorf("interruptions %d %v, want the member's approval alone", report.Interruptions, report.InterruptionsByKind)
	}
}

// A lane whose session never bound still has a record, opened at its end with what it ran on.
func TestALaneThatNeverBoundHasARecordToo(t *testing.T) {
	t.Parallel()
	r := laneRigWith(t, noRestarts)
	r.driver.Play(script([]fake.DriverStep{{Crash: true}}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	record := r.experience("lane-1")
	if record.Settings.Driver != "claude-code" || record.Settings.ModelResolved != "" || record.Outcome == nil ||
		record.Outcome.Status != string(workorders.OutcomeFailed) {
		t.Errorf("record %+v outcome %+v", record, record.Outcome)
	}
}
