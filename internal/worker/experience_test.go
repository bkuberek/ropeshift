package worker_test

import (
	"math"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/fake"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/lanes"
	"ropeshift.dev/ropeshift/internal/measures"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/workorders"
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
	r := newLaneRig(t)
	r.driver.Play(script([]fake.DriverStep{{Bound: true}, r.reports(workorders.OutcomeCompleted), completed(0.25)}))
	if err := r.subject.Converge(t.Context()); err != nil {
		t.Fatal(err)
	}
	record := r.experience("lane-1")
	want := measures.Settings{
		Driver: "claude-code", ModelRequested: "opus", ModelResolved: "resolved-model", Effort: "high", RunMode: ports.RunModeHeadless,
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

// A lane whose session never bound still has a record, opened at its end with what it ran on.
func TestALaneThatNeverBoundHasARecordToo(t *testing.T) {
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
