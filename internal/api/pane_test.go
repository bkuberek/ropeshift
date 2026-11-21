package api_test

import (
	"errors"
	"net/http"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
)

// A read of a pane's scrollback goes to the lane's pane route under the project, names the lane as one path
// segment, and sends no count when it asks for none, so the server's default applies.
func TestPaneScrollbackReadsTheLanesPaneRoute(t *testing.T) {
	t.Parallel()
	client, seen, _ := repositoryCreationServer(t, http.StatusOK, `{"lane_id":"lane:plan 1","generation":2,"lines":"one\ntwo\n","size":2,"limit":2000}`)
	got, err := client.PaneScrollback(t.Context(), "lane:plan 1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if seen.Method != http.MethodGet || seen.URL.EscapedPath() != "/v1/projects/tt/panes/lane:plan%201/scrollback" || seen.URL.RawQuery != "" {
		t.Errorf("the server saw %s %s?%s", seen.Method, seen.URL.EscapedPath(), seen.URL.RawQuery)
	}
	want := api.PaneScrollback{Lane: "lane:plan 1", Generation: 2, Lines: "one\ntwo\n", Size: 2, Limit: 2000}
	if got != want {
		t.Errorf("the read gave %+v, not %+v", got, want)
	}
}

// A count goes in the query as the number of lines asked for.
func TestPaneScrollbackAsksForTheCountOfLines(t *testing.T) {
	t.Parallel()
	client, seen, _ := repositoryCreationServer(t, http.StatusOK, `{"lane_id":"l1","generation":1,"lines":"","size":0,"limit":2000}`)
	if _, err := client.PaneScrollback(t.Context(), "l1", 50); err != nil {
		t.Fatal(err)
	}
	if seen.URL.Path != "/v1/projects/tt/panes/l1/scrollback" || seen.URL.Query().Get("lines") != "50" {
		t.Errorf("the server saw %s?%s", seen.URL.Path, seen.URL.RawQuery)
	}
}

// A refusal comes back as the API's own error, such as a parked lane that has no pane to read.
func TestPaneScrollbackReadsTheServersRefusal(t *testing.T) {
	t.Parallel()
	client, _, _ := repositoryCreationServer(t, http.StatusConflict, `{"error":{"code":"no_pane","message":"lane l1 has no pane"}}`)
	_, err := client.PaneScrollback(t.Context(), "l1", 0)
	var refused *api.Error
	if !errors.As(err, &refused) || refused.Status != http.StatusConflict || refused.Detail.Code != api.CodeNoPane {
		t.Errorf("the refusal reads %v", err)
	}
}
