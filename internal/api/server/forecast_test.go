package server_test

import (
	"net/http"
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/test/apitest"
)

// forecastTick runs the daemon's forecast tick once.
func forecastTick(t *testing.T, d *apitest.Daemon) {
	t.Helper()
	if _, err := control.NewForecasts(d.Store).Refresh(t.Context(), apitest.Scope); err != nil {
		t.Fatal(err)
	}
}

// TestAPlanVersionsForecastIsPendingUntilTheTickForecastsIt holds FR-EST-10: a version intake recorded has no
// forecast until the forecast tick forecasts it, and until then its forecast and its approval read "forecast
// pending", with no forecast and no estimate set; once the tick has run, both carry the forecasts it kept.
func TestAPlanVersionsForecastIsPendingUntilTheTickForecastsIt(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	w.Planned(retrySource("The first draft."))
	plan := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")).Approval.Plan
	var raw map[string]any
	if err := d.Client.Do(t.Context(), http.MethodGet, d.Client.Route(api.PlanForecastPath(plan, 0)), nil, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["state"] != api.ForecastPending || raw["estimate_set"] != nil || len(raw["forecasts"].([]any)) != 0 {
		t.Errorf("before the tick, the forecast reads %v", raw)
	}
	view, err := d.Client.Plan(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if f := view.Approval.Forecast; f == nil || f.State != api.ForecastPending || f.Version != 1 || len(f.Forecasts) != 0 || f.Set != nil {
		t.Errorf("before the tick, the approval's forecast reads %+v", f)
	}

	forecastTick(t, d)
	made, err := d.Client.PlanForecast(t.Context(), plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	if made.State != api.ForecastMade || made.Version != 1 || len(made.Forecasts) != 1 || made.Set == nil {
		t.Fatalf("after the tick, the forecast reads %+v", made)
	}
	if view, err = d.Client.Plan(t.Context(), plan); err != nil {
		t.Fatal(err)
	}
	if f := view.Approval.Forecast; f == nil || !reflect.DeepEqual(*f, made) {
		t.Errorf("after the tick, the approval's forecast reads %+v, not %+v", f, made)
	}
}

// TestApprovingWhileTheForecastIsPendingApprovesTheVersion shows that an approval does not wait for the tick: a
// member approves a version whose forecast is pending, and the tick forecasts it afterwards.
func TestApprovingWhileTheForecastIsPendingApprovesTheVersion(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	w.Planned(retrySource("The first draft."))
	need := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, ""))
	if _, err := d.Client.DecideApproval(t.Context(), need.Approval.ID, api.DecideApproval{Digest: need.Approval.SubjectDigest, Allow: true}); err != nil {
		t.Fatal(err)
	}
	view, err := d.Client.Plan(t.Context(), need.Approval.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if view.Approval.Approval.Open() || view.Approval.Forecast == nil || view.Approval.Forecast.State != api.ForecastPending {
		t.Errorf("approved while pending, the approval reads %+v with forecast %+v", view.Approval.Approval, view.Approval.Forecast)
	}
	forecastTick(t, d)
	if made, err := d.Client.PlanForecast(t.Context(), need.Approval.Plan, 1); err != nil || made.State != api.ForecastMade {
		t.Errorf("after the tick, the approved version's forecast reads %+v: %v", made, err)
	}
}

// TestAVersionReplacedBeforeTheTickForecastItHasNone holds that the tick forecasts only each plan's newest version:
// a version a newer one replaced before the tick ran is never forecast, so it reads as replaced and not pending.
func TestAVersionReplacedBeforeTheTickForecastItHasNone(t *testing.T) {
	t.Parallel()
	d, plan := approvedThenChanged(t)
	first, err := d.Client.PlanForecast(t.Context(), plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	newest, err := d.Client.PlanForecast(t.Context(), plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	if first.State != api.ForecastSuperseded || newest.State != api.ForecastPending || newest.Version != 2 {
		t.Errorf("version 1 reads %+v, and the newest %+v", first, newest)
	}
	view, err := d.Client.PlanVersion(t.Context(), plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	if f := view.Approval.Forecast; f == nil || f.State != api.ForecastSuperseded {
		t.Errorf("version 1's approval reads forecast %+v", f)
	}
}
