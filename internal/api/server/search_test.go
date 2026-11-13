package server_test

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/test/apitest"
)

// search reads the search read model with a query, as the command palette asks for it.
func search(t *testing.T, d *apitest.Daemon, query url.Values) api.Search {
	t.Helper()
	out, err := d.Client.Search(t.Context(), query)
	if err != nil {
		t.Fatalf("search %v: %v", query, err)
	}
	return out
}

func kindsOf(records []api.SearchRecord) []string {
	var kinds []string
	for _, record := range records {
		if !slices.Contains(kinds, record.Kind) {
			kinds = append(kinds, record.Kind)
		}
	}
	return kinds
}

// TestSearch reads the search read model of one world, which every case shares, because a world takes
// seconds to build under the race detector.
func TestSearch(t *testing.T) {
	t.Parallel()
	d := aDetailedWorld(t)
	// The palette finds a record by its ID, which ranks first, with its kind, title, and state.
	t.Run("finds a record by its ID", func(t *testing.T) {
		got := search(t, d, url.Values{"q": {"WO-EFFECT"}})
		if len(got.Records) == 0 {
			t.Fatal("found nothing")
		}
		first := got.Records[0]
		if first.Kind != api.SearchWorkOrder || first.ID != "wo-effect" || first.Title != "Add the retry budget" || first.State != "running" {
			t.Errorf("first %+v", first)
		}
		if got.AsOf == 0 || got.Revision == 0 {
			t.Errorf("stamp %+v", got.Stamp)
		}
	})
	// Words match in any order and any case, each against the ID or the title.
	t.Run("ignores case and word order", func(t *testing.T) {
		for _, q := range []string{"retry budget add", "BUDGET Retry", "  add   the retry  "} {
			got := search(t, d, url.Values{"q": {q}})
			found := slices.ContainsFunc(got.Records, func(record api.SearchRecord) bool { return record.ID == "wo-effect" })
			if !found {
				t.Errorf("%q did not find wo-effect: %+v", q, got.Records)
			}
			for _, record := range got.Records {
				text := strings.ToLower(record.ID + " " + record.Title)
				for _, word := range strings.Fields(strings.ToLower(q)) {
					if !strings.Contains(text, word) {
						t.Errorf("%q found %+v, which lacks %q", q, record, word)
					}
				}
			}
		}
	})
	// An ID that only begins with the words ranks after an exact one and before a title that holds them.
	t.Run("ranks an exact ID then a prefix then the rest", func(t *testing.T) {
		got := search(t, d, url.Values{"q": {"decision-1"}})
		if len(got.Records) == 0 || got.Records[0].ID != "decision-1" || got.Records[0].Kind != api.SearchDecision {
			t.Fatalf("records %+v", got.Records)
		}
		prefix := search(t, d, url.Values{"q": {"decision-"}})
		for i, record := range prefix.Records {
			if !strings.HasPrefix(record.ID, "decision-") && i < 3 {
				t.Errorf("record %d %+v ranks among the IDs that begin with the words", i, record)
			}
		}
	})
	// Every kind of record the design names, that this ledger holds, is found, each with a title.
	t.Run("lists every kind with no words", func(t *testing.T) {
		got := search(t, d, nil)
		kinds := kindsOf(got.Records)
		for _, kind := range []string{api.SearchWorkOrder, api.SearchLane, api.SearchPlan, api.SearchContract, api.SearchRequirement, api.SearchDecision, api.SearchQuestion, api.SearchRepository} {
			if !slices.Contains(kinds, kind) {
				t.Errorf("no %s among %v", kind, kinds)
			}
		}
		if got.More {
			t.Error("a list with no words is whole")
		}
	})
	// The category a command line passes names one kind, and only that kind is listed.
	t.Run("narrows to one kind", func(t *testing.T) {
		got := search(t, d, url.Values{"category": {api.SearchQuestion}})
		if kinds := kindsOf(got.Records); len(kinds) != 1 || kinds[0] != api.SearchQuestion {
			t.Errorf("kinds %v", kinds)
		}
	})
	// A limit cuts the list and says it did, so a page can say more matched.
	t.Run("cuts at its limit and says so", func(t *testing.T) {
		got := search(t, d, url.Values{"q": {"wo"}, "limit": {"3"}})
		if len(got.Records) != 3 || !got.More {
			t.Errorf("%d records, more %v", len(got.Records), got.More)
		}
		words := search(t, d, url.Values{"q": {"wo"}})
		if len(words.Records) > api.SearchLimit {
			t.Errorf("%d records over the default limit of %d", len(words.Records), api.SearchLimit)
		}
	})
	// A limit that is no number is the caller's mistake, refused as one.
	t.Run("refuses a limit that is no number", func(t *testing.T) {
		var out api.Search
		err := d.Client.Do(t.Context(), http.MethodGet, d.Client.Route(api.ViewPath(api.ViewSearch)+"?limit=many"), nil, &out)
		if err == nil || !strings.Contains(err.Error(), "limit") {
			t.Errorf("err %v", err)
		}
	})
}

// The web listener serves the search, and the command that does the same is `view search`.
func TestTheWebListenerServesTheSearch(t *testing.T) {
	t.Parallel()
	want := api.ProjectRoute(api.ViewPath(api.ViewSearch))
	found := slices.ContainsFunc(api.WebRoutes(), func(route api.WebRoute) bool {
		return route.Pattern == want && route.Command == "view "+api.ViewSearch
	})
	if !found {
		t.Errorf("no web route %s", want)
	}
}
