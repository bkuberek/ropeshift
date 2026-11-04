package measures

// Point is one measure as a metric, which the measures tick exports as `ropeshift.measure.<name>`.
// Labels say what part of a measure a point is.
type Point struct {
	Name   string
	Unit   string
	Value  float64
	Labels map[string]string
}

// Points lists a report's measures as metrics. A rate with nothing under it has no value and no
// point, because a rate of nothing is not zero.
func Points(r Report) []Point {
	var out []Point
	add := func(name, unit string, value float64, labels map[string]string) {
		out = append(out, Point{Name: name, Unit: unit, Value: value, Labels: labels})
	}
	rate := func(name string, r Ratio) {
		if r.Value != nil {
			add(name, "1", *r.Value, nil)
		}
	}
	add("interruptions", "{interruption}", float64(r.Interruptions), nil)
	add("completed", "{work_order}", float64(r.Completed), nil)
	rate("interruptions_per_completed", r.InterruptionsPerCompleted)
	rate("overturn_rate", r.OverturnRate)
	rate("rework_rate", r.Rework)
	rate("autonomous_decision_rate", r.AutonomousDecisionRate)
	rate("repeated_question_rate", r.RepeatedQuestionRate)
	add("prevented_repeats", "{question}", float64(r.PreventedRepeats), nil)
	add("work_in_progress", "{work_order}", float64(r.WorkInProgress), nil)
	durationPoints := func(name string, d Durations, labels map[string]string) {
		if d.MedianMs == nil {
			return
		}
		for _, p := range []struct {
			at    string
			value int64
		}{{"median", *d.MedianMs}, {"p95", *d.P95Ms}} {
			withAt := map[string]string{"percentile": p.at}
			for key, value := range labels {
				withAt[key] = value
			}
			add(name, "s", float64(p.value)/1000, withAt)
		}
	}
	for trigger, d := range r.ParkTime {
		durationPoints("park_time", d, map[string]string{"trigger": trigger})
	}
	durationPoints("flow_time", r.ReadyToDone, map[string]string{"from": "ready"})
	durationPoints("flow_time", r.IntakeToDone, map[string]string{"from": "intake"})
	for kind, cost := range r.CostPerOutcome {
		if cost.PerDone != nil {
			lower := "false"
			if cost.LowerBound {
				lower = "true"
			}
			add("cost_per_outcome", "USD", *cost.PerDone, map[string]string{"kind": kind, "lower_bound": lower})
		}
	}
	return out
}
