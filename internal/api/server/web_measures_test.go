package server_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/delivery"
	"ropeshift.com/ropeshift/test/apitest"
)

// signedInTo is a member signed in to the web listener of a daemon a test has already filled.
func signedInTo(t *testing.T, d *apitest.Daemon) browser {
	t.Helper()
	r := webOn(t, d)
	cookies, token := r.signedIn(t)
	return browser{r: r, cookies: cookies, headers: map[string]string{api.HeaderCSRF: token, "Origin": "http://" + r.web.Host}}
}

// Delivery look back and a plan's forecast are the API's own reads, behind the web listener's session
// (FR-UI-7). These hold what the Measures view and the plan pages rely on: a member's session reads what the
// command line prints, in the same words, and the API's refusals are the words the page shows.

func TestAMemberReadsDeliveryLookBackFromTheBrowser(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	for _, by := range api.DeliveryGroupings {
		var got delivery.LookBack
		if err := json.Unmarshal(b.send(t, http.MethodGet, api.DeliveryPath+"?window=7d&by="+by, nil, http.StatusOK), &got); err != nil {
			t.Fatal(err)
		}
		want, err := b.r.d.Client.Delivery(t.Context(), "7d", by)
		if err != nil {
			t.Fatal(err)
		}
		// The window ends at the daemon's clock, so two reads differ in the milliseconds they cover and in nothing
		// they hold.
		keys := func(read delivery.LookBack) []string {
			out := []string{}
			for _, group := range read.Groups {
				out = append(out, group.Key)
			}
			return out
		}
		if got.By != by || len(got.Groups) == 0 || len(got.Groups[0].Trend) != len(want.Groups[0].Trend) || !reflect.DeepEqual(keys(got), keys(want)) {
			t.Errorf("by %s the browser reads %+v, the API %+v", by, got, want)
		}
	}
	// Asked for no window or grouping, the browser reads what the command prints: 30 days, by kind.
	var defaults delivery.LookBack
	if err := json.Unmarshal(b.send(t, http.MethodGet, api.DeliveryPath, nil, http.StatusOK), &defaults); err != nil || defaults.By != delivery.ByKind {
		t.Errorf("by default the browser reads %+v: %v", defaults, err)
	}
}

func TestTheLedgerRefusesALookBackTheBrowserAsksForWrongly(t *testing.T) {
	t.Parallel()
	b := signedInBrowser(t)
	wantCode(t, b.send(t, http.MethodGet, api.DeliveryPath+"?by=colour", nil, http.StatusBadRequest), api.CodeInvalid)
	wantCode(t, b.send(t, http.MethodGet, api.DeliveryPath+"?window=soon", nil, http.StatusBadRequest), api.CodeInvalid)
}

func TestAMemberReadsAPlansForecastFromTheBrowser(t *testing.T) {
	t.Parallel()
	d, w, _ := aWorld(t)
	b := signedInTo(t, d)
	w.Planned(retrySource("The first draft."))
	plan := planApprovalNeed(t, readView[api.NeedsYou](t, d, api.ViewNeedsYou, "")).Approval.Plan

	var pending api.PlanForecast
	if err := json.Unmarshal(b.send(t, http.MethodGet, api.PlanForecastPath(plan, 0), nil, http.StatusOK), &pending); err != nil {
		t.Fatal(err)
	}
	if pending.State != api.ForecastPending || pending.Plan != plan || pending.Version != 1 || len(pending.Forecasts) != 0 {
		t.Errorf("before the tick the browser reads %+v", pending)
	}

	forecastTick(t, d)
	var made api.PlanForecast
	if err := json.Unmarshal(b.send(t, http.MethodGet, api.PlanForecastPath(plan, 1), nil, http.StatusOK), &made); err != nil {
		t.Fatal(err)
	}
	want, err := d.Client.PlanForecast(t.Context(), plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	if made.State != api.ForecastMade || len(made.Forecasts) == 0 || !reflect.DeepEqual(made, want) {
		t.Errorf("after the tick the browser reads %+v, the API %+v", made, want)
	}

	wantCode(t, b.send(t, http.MethodGet, api.PlanForecastPath(plan, 9), nil, http.StatusNotFound), api.CodeNotFound)
	wantCode(t, b.send(t, http.MethodGet, api.PlanForecastPath("no-such-plan", 0), nil, http.StatusNotFound), api.CodeNotFound)
	wantCode(t, b.send(t, http.MethodGet, "/v1/plans/"+plan+"/forecast?version=0", nil, http.StatusBadRequest), api.CodeInvalid)
}
