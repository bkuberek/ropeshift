package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/apitest"
)

func scrollbackPath(lane, query string) string {
	return apitest.Route(strings.ReplaceAll(api.PaneScrollbackPath, "{id}", lane)) + query
}

// A member reads the newest lines of a lane's scrollback, with how many lines the history holds now and how
// many tmux keeps, so a page can load older lines as the member scrolls up and say how much there is.
func TestAMemberReadsALanesScrollback(t *testing.T) {
	t.Parallel()
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "a screen")
	r.panes.remember(r.lane.ID, 1, "one", "two", "three")
	cookies, _ := r.signedIn(t)
	for _, c := range []struct {
		query string
		want  api.PaneScrollback
	}{
		{"?lines=2", api.PaneScrollback{Lane: r.lane.ID, Generation: 1, Lines: "two\nthree\n", Size: 3, Limit: 2000}},
		{"", api.PaneScrollback{Lane: r.lane.ID, Generation: 1, Lines: "one\ntwo\nthree\n", Size: 3, Limit: 2000}},
	} {
		response, raw := r.send(t, call{path: scrollbackPath(r.lane.ID, c.query), cookies: cookies})
		wantStatus(t, response, raw, http.StatusOK, "")
		var got api.PaneScrollback
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%q: scrollback %+v, want %+v", c.query, got, c.want)
		}
	}
}

// A read asks for at least one line and at most the bound, as a number, once.
func TestTheScrollbackRefusesALineCountOutOfBounds(t *testing.T) {
	t.Parallel()
	r := paneOn(t)
	r.panes.show(r.lane.ID, 1, "a screen")
	cookies, _ := r.signedIn(t)
	for _, query := range []string{"?lines=0", "?lines=-1", "?lines=many", "?lines=1.5", fmt.Sprintf("?lines=%d", api.MaxPaneScrollback+1), "?lines=1&lines=2"} {
		response, raw := r.send(t, call{path: scrollbackPath(r.lane.ID, query), cookies: cookies})
		wantStatus(t, response, raw, http.StatusBadRequest, api.CodeInvalid)
	}
}

// Only a member's session reads a scrollback, and a lane with no pane to read says why, as its screen does.
func TestTheScrollbackRefusesALaneWithNoPaneAndAnyoneWithNoSession(t *testing.T) {
	t.Parallel()
	r := paneOn(t)
	response, raw := r.send(t, call{path: scrollbackPath(r.lane.ID, "")})
	wantStatus(t, response, raw, http.StatusUnauthorized, api.CodeSessionRequired)
	cookies, _ := r.signedIn(t)
	ended := r.world.Running("wo-ended", "A lane that ended")
	ended.End(workorders.OutcomeCompleted, "Done")
	parked := r.world.Running("wo-parked", "A lane that parked")
	parked.Park("after 1h")
	for _, c := range []struct {
		name   string
		lane   string
		status int
		code   string
	}{
		{"a lane with no pane", r.lane.ID, http.StatusConflict, api.CodeNoPane},
		{"a parked lane", parked.ID, http.StatusConflict, api.CodeNoPane},
		{"an ended lane", ended.ID, http.StatusConflict, api.CodeLaneEnded},
		{"no such lane", "lane-none", http.StatusNotFound, api.CodeNotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			response, raw := r.send(t, call{path: scrollbackPath(c.lane, ""), cookies: cookies})
			wantStatus(t, response, raw, c.status, c.code)
		})
	}
}
