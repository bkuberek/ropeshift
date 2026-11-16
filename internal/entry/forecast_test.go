package entry

import (
	"slices"
	"strings"
	"testing"
	"time"

	"ropeshift.com/ropeshift/internal/adapters/fake"
	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
	"ropeshift.com/ropeshift/test/contract"
)

// TestTheForecastTickForecastsEachProjectsPlans runs the daemon's forecast tick, on its own schedule, over a
// project with a plan: the tick keeps the plan version's forecast, apart from any request (ADR-0026, FR-EST-8).
func TestTheForecastTickForecastsEachProjectsPlans(t *testing.T) {
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	ctx := t.Context()
	state, home, err := sqlite.Upgrade(ctx, t.TempDir(), "ledger.db", ledger.Workspace{ID: soloScope, Name: "Solo"}, member, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	defer home.Close()
	source := fake.NewWorkSource()
	source.Write("goal.md", []byte(contract.GoalText("a-goal", "The goal")))
	source.Write("plan.md", []byte(contract.PlanText("a-plan", "The plan", "a-goal")))
	source.Write("requirements.md", []byte(contract.RequirementsText("payments", "PAY-12", "It retries")))
	source.Write("budget.md", []byte(strings.Replace(contract.ContractText("budget", "The budget", "The budget."), `title = "The budget"`, "title = \"The budget\"\nplan = \"a-plan\"", 1)))
	if report, err := control.NewIntake(home).Poll(ctx, soloScope, source); err != nil || len(report.Problems) != 0 {
		t.Fatalf("intake read %+v: %v", report, err)
	}
	if opts, ok := parseSolo(nil, &strings.Builder{}); !ok || opts.forecastTick != time.Minute {
		t.Errorf("the forecast tick runs every %s by default", opts.forecastTick)
	}
	ticks := soloTicks(state, soloScope, soloOptions{forecastTick: time.Minute}, fake.NewCheckouts(), questions.BatchPolicy{}, nil)
	at := slices.IndexFunc(ticks, func(tick control.Tick) bool { return tick.Name == "forecast" })
	if at < 0 || ticks[at].Every != time.Minute {
		t.Fatalf("the forecast tick is %+v", ticks)
	}
	if err := ticks[at].Run(ctx); err != nil {
		t.Fatal(err)
	}
	var forecasts []delivery.Forecast
	err = home.Read(ctx, soloScope, func(tx ports.ReadTx) error {
		plans, err := tx.Planning().Plans()
		if err != nil || len(plans) != 1 {
			t.Fatalf("plans %+v: %v", plans, err)
		}
		forecasts, err = tx.Forecasts().Latest(plans[0].ID, 1)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(forecasts) != 1 || forecasts[0].Milestone != "" || forecasts[0].Status != delivery.ForecastNoCapacity {
		t.Errorf("after the tick, the plan's forecasts are %+v", forecasts)
	}
}
