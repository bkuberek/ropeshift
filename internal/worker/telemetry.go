package worker

import (
	"path/filepath"
	"time"

	"ropeshift.com/ropeshift/internal/lanes"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/measures"
	"ropeshift.com/ropeshift/internal/ports"
)

// LaneTelemetry is where a lane's agent sends its telemetry: the worker's own receiver (FR-OBS-3). An
// empty endpoint tells no session anything, which is what a worker with no receiver does.
type LaneTelemetry struct {
	Endpoint  string
	Header    string
	Workspace string
}

// noTelemetry is what a worker with no Telemetry emits to.
type noTelemetry struct{}

func (noTelemetry) Span(ports.Span)           {}
func (noTelemetry) Measure(ports.Measurement) {}
func (w *Worker) telemetry() ports.Telemetry  { return w.config.Telemetry }
func (w *Worker) nowMs() int64                { return w.clock.Now().UnixMilli() }

// sessionTelemetry tags one generation's telemetry with what produced it (FR-OBS-2). The repository's
// name is its checkout's, and a profile can keep it and the branch out. Content stays out unless the
// profile says otherwise, and only the user's layer can (FR-OBS-5).
func (r *laneRun) sessionTelemetry(prepared PreparedLane, repository string) ports.SessionTelemetry {
	t := r.rt.Telemetry
	if t.Endpoint == "" {
		return ports.SessionTelemetry{}
	}
	settings := prepared.Profile.Telemetry
	// The lane's project is the workspace its records carry. A plane of one project names none, and the
	// worker's own workspace stands for it.
	project := r.lease.Project
	if project == "" {
		project = t.Workspace
	}
	tags := lanes.TelemetryTags{
		Project: project, Workspace: project, WorkOrder: r.lease.WorkOrder, Lane: r.lease.Lane, Role: r.role(), Generation: r.currentGeneration(),
		Profile: prepared.Profile.Name, Repo: filepath.Base(repository), Branch: prepared.Worktree.Branch, Worker: r.w.config.Name,
		Driver: prepared.Profile.Driver,
	}
	return ports.SessionTelemetry{
		Endpoint: t.Endpoint, Header: t.Header, Tags: tags.Omitting(settings), Traces: settings.Traces, Content: settings.Content,
	}
}

// measureEffect records one external effect as a span under its generation, with how long it took. A
// detail can hold a path or a command's words, so the span carries only the effect's kind and state.
func (w *Worker) measureEffect(effect lanes.Effect, state lanes.EffectState, started time.Time) {
	w.mu.Lock()
	run := w.running[effect.Lane]
	w.mu.Unlock()
	trace := ledger.TraceID("lane", effect.Lane)
	attributes := []ports.Attribute{
		{Key: lanes.AttrLane, Value: effect.Lane}, {Key: lanes.AttrGeneration, Value: effect.Generation},
		{Key: ledger.AttrEffectKind, Value: string(effect.Kind)}, {Key: ledger.AttrEffectState, Value: string(state)},
	}
	if run != nil {
		trace = ledger.WorkOrderTrace(run.lease.WorkOrder)
		attributes = append(attributes, ports.Attribute{Key: lanes.AttrWorkOrder, Value: run.lease.WorkOrder})
	}
	end := w.clock.Now()
	w.telemetry().Span(ports.Span{
		Name: ledger.SpanEffect, Kind: ports.SpanInternal, TraceID: trace,
		SpanID:   ledger.SpanID("effect", effect.Key, started.Format(time.RFC3339Nano)),
		ParentID: ledger.GenerationSpan(effect.Lane, effect.Generation), Start: started.UnixMilli(), End: end.UnixMilli(),
		Failed: state == lanes.EffectFailed, Attributes: attributes,
	})
	w.telemetry().Measure(ports.Measurement{
		Name: ledger.MetricEffectDuration, Kind: ports.MetricHistogram, Unit: "ms", Value: float64(end.Sub(started).Milliseconds()),
		At: end.UnixMilli(), Attributes: attributes,
	})
}

// laneSettings are what a lane runs on, as its experience record holds them (FR-EXP-2): the agent, the
// model, the effort, the run mode, and the context thresholds of its role. The models and the work's
// size join them later, because only a binding and the ledger know those.
func laneSettings(profile lanes.Profile, role, runMode string) measures.Settings {
	thresholds := profile.Context.ThresholdsFor(role)
	return measures.Settings{
		Driver: profile.Driver, ModelRequested: profile.Agent.Model, Effort: profile.Agent.Effort, RunMode: runMode,
		Context: measures.ContextSettings{
			Role: role, WarnTokens: thresholds.WarnTokens, CompactTokens: thresholds.CompactTokens, HandoffTokens: thresholds.HandoffTokens,
		},
	}
}

// endSettings are this lane's settings as its end reports them, for a record its binding never opened.
// A run that never reached its spec reports none.
func (r *laneRun) endSettings() *measures.Settings {
	r.handleLock.Lock()
	defer r.handleLock.Unlock()
	if r.settings.Driver == "" {
		return nil
	}
	settings := r.settings
	settings.ModelResolved, settings.ProfileDigest = r.resolved, r.profileDigest
	return &settings
}

// laneAttributes are what every per-lane measurement carries, so it joins the agent's own data.
func (r *laneRun) laneAttributes() []ports.Attribute {
	return []ports.Attribute{
		{Key: lanes.AttrLane, Value: r.lease.Lane}, {Key: lanes.AttrWorkOrder, Value: r.lease.WorkOrder},
		{Key: lanes.AttrGeneration, Value: r.currentGeneration()},
	}
}

func (r *laneRun) measure(name string, kind ports.MetricKind, unit string, value float64, extra ...ports.Attribute) {
	r.w.telemetry().Measure(ports.Measurement{
		Name: name, Kind: kind, Unit: unit, Value: value, At: r.w.nowMs(), Attributes: append(r.laneAttributes(), extra...),
	})
}

// measureContext records what the session held at one API call, and its baseline when this call set it.
func (r *laneRun) measureContext(tokens, baseline int64) {
	r.measure(ledger.MetricContextTokens, ports.MetricGauge, "{token}", float64(tokens))
	if baseline != 0 {
		r.measure(ledger.MetricContextBaseline, ports.MetricGauge, "{token}", float64(baseline))
	}
}

// measureTurn records a turn's tokens by type and its cost. The cost is the turn's share of the
// session's running total, a difference between totals, because a total accumulates across resumes.
// A turn a kill stopped reports nothing, so a sum of these is a lower bound.
func (r *laneRun) measureTurn(turn *ports.TurnResult) {
	if turn == nil {
		return
	}
	for kind, tokens := range map[string]int64{
		"input": turn.Usage.InputTokens, "output": turn.Usage.OutputTokens,
		"cache_read": turn.Usage.CacheReadTokens, "cache_write": turn.Usage.CacheCreationTokens,
	} {
		if tokens != 0 {
			r.measure(ledger.MetricTokens, ports.MetricCounter, "{token}", float64(tokens), ports.Attribute{Key: ledger.AttrTokenType, Value: kind})
		}
	}
	if turn.CostUSD != 0 {
		r.measure(ledger.MetricCost, ports.MetricCounter, "USD", turn.CostUSD)
	}
}
