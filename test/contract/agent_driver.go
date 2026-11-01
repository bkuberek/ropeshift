package contract

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"ropeshift.dev/ropeshift/internal/ports"
)

// The scenarios every driver must answer. An adapter plays each from a fixture, and
// the fake from a script, so the fake cannot drift from the driver it stands for.
const (
	ScenarioTwoTurns   = "two-turns"
	ScenarioStopped    = "stopped-turn"
	ScenarioCrash      = "crash-mid-turn"
	ScenarioMalformed  = "malformed-line"
	ScenarioFailedTurn = "failed-turn"
	ScenarioSubagent   = "subagent-usage"
)

// DriverRig is one driver, and how it prepares a scenario.
type DriverRig struct {
	Driver ports.AgentDriver
	// Arrange makes a scenario the next session, and returns the spec that starts it.
	Arrange func(scenario string) ports.SessionSpec
}

// RunAgentDriver runs one contract on one driver.
func RunAgentDriver(t *testing.T, fresh func(*testing.T) DriverRig) {
	t.Helper()
	checks := []struct {
		name string
		run  func(*testing.T, context.Context, DriverRig)
	}{
		{"capabilities are declared", checkDriverCapabilities},
		{"a spec with no permission mode does not pass", checkPreflight},
		{"session and model are read on every turn", checkBoundEveryTurn},
		{"context is every token sent, on one call", checkContextFormula},
		{"a child session's usage is not the lane's context", checkChildUsage},
		{"cost is a difference of running totals", checkCostDifference},
		{"a turn ends at its result, and an interrupt stops it", checkInterruptedTurn},
		{"no result is a crash", checkNoResult},
		{"a failed result is a failed turn and not a crash", checkFailedTurn},
		{"a line that does not parse is counted and survived", checkMalformedLine},
		{"a session stopped at rest ended cleanly", checkStopAtRest},
		{"an ended session reads dead and takes no input", checkEndedSession},
		{"an unknown handle reads unknown, not dead", checkUnknownHandle},
		{"a session that never started is not found", checkFindNothing},
		{"an adoption with nothing to take over refuses", checkAdoptNothing},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			check.run(t, ctx, fresh(t))
		})
	}
}

func checkFindNothing(t *testing.T, ctx context.Context, rig DriverRig) {
	spec := rig.Arrange(ScenarioTwoTurns)
	spec.Lane = "no-such-lane"
	located, err := rig.Driver.Find(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if located.Found || located.Readable {
		t.Errorf("located %+v", located)
	}
}

func checkAdoptNothing(t *testing.T, ctx context.Context, rig DriverRig) {
	err := rig.Driver.Adopt(ctx, ports.SessionHandle{ID: "no-such-lane/1"}, ports.AdoptFrom{})
	if !errors.Is(err, ports.ErrNoSession) {
		t.Errorf("adopt: %v", err)
	}
}

// driverSession is one started session and its stream.
type driverSession struct {
	t      *testing.T
	ctx    context.Context
	rig    DriverRig
	handle ports.SessionHandle
	events <-chan ports.DriverEvent
}

func startScenario(t *testing.T, ctx context.Context, rig DriverRig, scenario string) *driverSession {
	t.Helper()
	handle, err := rig.Driver.Start(ctx, rig.Arrange(scenario))
	if err != nil {
		t.Fatal(err)
	}
	events, err := rig.Driver.Events(ctx, handle)
	if err != nil {
		t.Fatal(err)
	}
	s := &driverSession{t: t, ctx: ctx, rig: rig, handle: handle, events: events}
	t.Cleanup(func() { _ = rig.Driver.Stop(context.Background(), handle, ports.StopNow) })
	return s
}

// until reads the stream up to an event of one kind.
func (s *driverSession) until(kind ports.EventKind) ports.DriverEvent {
	s.t.Helper()
	for {
		select {
		case event, open := <-s.events:
			if !open {
				s.t.Fatalf("the stream closed before %s", kind)
			}
			if event.Kind == kind {
				return event
			}
		case <-s.ctx.Done():
			s.t.Fatalf("no %s", kind)
		}
	}
}

func (s *driverSession) ended() ports.SessionEnd {
	s.t.Helper()
	event := s.until(ports.EventSessionEnded)
	if event.End == nil {
		s.t.Fatal("an end with no detail")
	}
	return *event.End
}

func (s *driverSession) send(text string) {
	s.t.Helper()
	receipt, err := s.rig.Driver.Send(s.ctx, s.handle, ports.Input{Text: text})
	if err != nil {
		s.t.Fatal(err)
	}
	if receipt.Path != ports.SendStdin {
		s.t.Errorf("receipt %+v", receipt)
	}
}

func checkDriverCapabilities(t *testing.T, ctx context.Context, rig DriverRig) {
	got, err := rig.Driver.Capabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version == "" || !slices.Contains(got.RunModes, ports.RunModeHeadless) {
		t.Errorf("capabilities %+v", got)
	}
	if got.ApprovalChannel != ports.ApprovalHook || got.ContextReport != ports.ContextLive || got.Compact != ports.CompactNative ||
		got.Injection != ports.InjectionBetweenTools || !got.NativeResume {
		t.Errorf("capabilities %+v", got)
	}
}

func checkPreflight(t *testing.T, ctx context.Context, rig DriverRig) {
	spec := rig.Arrange(ScenarioTwoTurns)
	report, err := rig.Driver.Preflight(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) != 0 {
		t.Fatalf("a good spec: %v", report.Problems)
	}
	spec.PermissionMode = ""
	report, err = rig.Driver.Preflight(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Problems) == 0 {
		t.Error("no permission mode passed")
	}
}

func checkBoundEveryTurn(t *testing.T, ctx context.Context, rig DriverRig) {
	s := startScenario(t, ctx, rig, ScenarioTwoTurns)
	first := s.until(ports.EventSessionBound)
	if first.SessionID == "" || first.ModelResolved == "" {
		t.Errorf("first turn %+v", first)
	}
	s.until(ports.EventTurnEnded)
	s.send("again")
	second := s.until(ports.EventSessionBound)
	if second.SessionID != first.SessionID || second.ModelResolved != first.ModelResolved {
		t.Errorf("second turn %+v", second)
	}
}

func checkContextFormula(t *testing.T, ctx context.Context, rig DriverRig) {
	s := startScenario(t, ctx, rig, ScenarioTwoTurns)
	if got := s.until(ports.EventUsage).ContextTokens; got != 1003 {
		t.Errorf("context %d", got)
	}
	s.until(ports.EventTurnEnded)
	observed, err := rig.Driver.Observe(ctx, s.handle)
	if err != nil {
		t.Fatal(err)
	}
	if observed.ContextTokens != 1003 {
		t.Errorf("observed %+v", observed)
	}
}

func checkChildUsage(t *testing.T, ctx context.Context, rig DriverRig) {
	s := startScenario(t, ctx, rig, ScenarioSubagent)
	if got := s.until(ports.EventUsage).ContextTokens; got != 11 {
		t.Errorf("context %d", got)
	}
	s.until(ports.EventTurnEnded)
	observed, err := rig.Driver.Observe(ctx, s.handle)
	if err != nil {
		t.Fatal(err)
	}
	if observed.ContextTokens != 11 {
		t.Errorf("a child's call moved the context to %d", observed.ContextTokens)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func checkCostDifference(t *testing.T, ctx context.Context, rig DriverRig) {
	s := startScenario(t, ctx, rig, ScenarioTwoTurns)
	first := s.until(ports.EventTurnEnded).Turn
	if first.Status != ports.TurnCompleted || !near(first.CostUSD, 0.1) {
		t.Errorf("first turn %+v", first)
	}
	s.send("again")
	second := s.until(ports.EventTurnEnded).Turn
	if !near(second.CostUSD, 0.15) || !near(second.Cost.CostUSD, 0.25) || !near(second.Cost.SessionTotalUSD, 0.25) {
		t.Errorf("second turn %+v", second)
	}
}

func checkInterruptedTurn(t *testing.T, ctx context.Context, rig DriverRig) {
	s := startScenario(t, ctx, rig, ScenarioStopped)
	s.until(ports.EventUsage)
	if err := rig.Driver.Stop(ctx, s.handle, ports.StopInterrupt); err != nil {
		t.Fatal(err)
	}
	turn := s.until(ports.EventTurnEnded).Turn
	if turn.Status != ports.TurnStopped || turn.Cost.UnknownTurns != 1 {
		t.Errorf("turn %+v", turn)
	}
	if end := s.ended(); end.Crashed {
		t.Errorf("a stopped turn read as a crash: %+v", end)
	}
}

func checkNoResult(t *testing.T, ctx context.Context, rig DriverRig) {
	s := startScenario(t, ctx, rig, ScenarioCrash)
	if end := s.ended(); !end.Crashed {
		t.Errorf("no result read as a clean end: %+v", end)
	}
}

func checkFailedTurn(t *testing.T, ctx context.Context, rig DriverRig) {
	s := startScenario(t, ctx, rig, ScenarioFailedTurn)
	turn := s.until(ports.EventTurnEnded).Turn
	if turn.Status != ports.TurnFailed || turn.Reason == "" {
		t.Errorf("turn %+v", turn)
	}
	if err := rig.Driver.Stop(ctx, s.handle, ports.StopAtRest); err != nil {
		t.Fatal(err)
	}
	if end := s.ended(); end.Crashed {
		t.Errorf("end %+v", end)
	}
}

func checkMalformedLine(t *testing.T, ctx context.Context, rig DriverRig) {
	s := startScenario(t, ctx, rig, ScenarioMalformed)
	s.until(ports.EventMalformed)
	if turn := s.until(ports.EventTurnEnded).Turn; turn.Status != ports.TurnCompleted {
		t.Errorf("turn %+v", turn)
	}
	observed, err := rig.Driver.Observe(ctx, s.handle)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Malformed != 1 {
		t.Errorf("observed %+v", observed)
	}
}

func checkStopAtRest(t *testing.T, ctx context.Context, rig DriverRig) {
	s := startScenario(t, ctx, rig, ScenarioTwoTurns)
	s.until(ports.EventTurnEnded)
	if err := rig.Driver.Stop(ctx, s.handle, ports.StopAtRest); err != nil {
		t.Fatal(err)
	}
	if end := s.ended(); end.Crashed || end.Exit.Code != 0 {
		t.Errorf("end %+v", end)
	}
}

func checkEndedSession(t *testing.T, ctx context.Context, rig DriverRig) {
	s := startScenario(t, ctx, rig, ScenarioTwoTurns)
	observed, err := rig.Driver.Observe(ctx, s.handle)
	if err != nil {
		t.Fatal(err)
	}
	if observed.Liveness != ports.LivenessAlive {
		t.Errorf("a live session reads %s", observed.Liveness)
	}
	s.until(ports.EventTurnEnded)
	if err := rig.Driver.Stop(ctx, s.handle, ports.StopAtRest); err != nil {
		t.Fatal(err)
	}
	s.ended()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if observed, err = rig.Driver.Observe(ctx, s.handle); err != nil {
			t.Fatal(err)
		}
		if observed.Liveness == ports.LivenessDead {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if observed.Liveness != ports.LivenessDead {
		t.Errorf("an ended session reads %s", observed.Liveness)
	}
	if _, err := rig.Driver.Send(ctx, s.handle, ports.Input{Text: "late"}); !errors.Is(err, ports.ErrNoSession) {
		t.Errorf("a send to an ended session: %v", err)
	}
}

func checkUnknownHandle(t *testing.T, ctx context.Context, rig DriverRig) {
	observed, err := rig.Driver.Observe(ctx, ports.SessionHandle{ID: "nothing"})
	if !errors.Is(err, ports.ErrNoSession) || observed.Liveness != ports.LivenessUnknown {
		t.Errorf("observed %+v, %v", observed, err)
	}
}
