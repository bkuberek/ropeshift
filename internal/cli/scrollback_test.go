package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
)

const scrollbackRoute = "GET /v1/panes/{id}/scrollback"

// scrolled answers a read of a pane's scrollback with lines, and keeps the lane and the count the read named.
func scrolled(p *plane, history api.PaneScrollback) (lane, lines *string) {
	lane, lines = new(string), new(string)
	p.on(scrollbackRoute, func(r *http.Request, _ []byte) (any, *api.Error) {
		*lane, *lines = r.PathValue("id"), r.URL.Query().Get("lines")
		return history, nil
	})
	return lane, lines
}

// The scrollback is what an agent printed, so the command prints it as plain text, one line to a line, with no
// escape sequence that could act on the person's terminal.
func TestLaneScrollbackPrintsTheLinesAsPlainText(t *testing.T) {
	p := newPlane(t)
	lane, lines := scrolled(p, api.PaneScrollback{Lane: "lane-1", Generation: 2, Lines: "\x1b[1mone\x1b[0m\n\x1b]0;a title\x07two\n", Size: 2, Limit: 2000})
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"lane", "scrollback", "lane-1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if got := stdout.String(); got != "one\ntwo\n" {
		t.Errorf("printed %q", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("a read that shows every line said %q", stderr.String())
	}
	if *lane != "lane-1" || *lines != "200" {
		t.Errorf("the read named lane %q and %q lines, not lane-1 and the default 200", *lane, *lines)
	}
}

// --lines asks for that many of the newest lines, and when the pane keeps more, stderr says so, so stdout holds only
// the scrollback.
func TestLaneScrollbackAsksForTheCountAndSaysWhenThereIsMore(t *testing.T) {
	p := newPlane(t)
	_, lines := scrolled(p, api.PaneScrollback{Lane: "lane-1", Generation: 1, Lines: "four\nfive\n", Size: 5, Limit: 2000})
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"lane", "scrollback", "lane-1", "--lines", "2"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if *lines != "2" {
		t.Errorf("the read asked for %q lines, not 2", *lines)
	}
	if got := stdout.String(); got != "four\nfive\n" {
		t.Errorf("printed %q", got)
	}
	if !strings.Contains(stderr.String(), "the newest 2 of the 5 lines") || !strings.Contains(stderr.String(), "--lines") {
		t.Errorf("stderr does not say it showed 2 of 5 lines and how to ask for more: %q", stderr.String())
	}
}

// A pane with no scrollback yet prints nothing, and says so on stderr.
func TestLaneScrollbackSaysAPaneHoldsNoScrollbackYet(t *testing.T) {
	p := newPlane(t)
	scrolled(p, api.PaneScrollback{Lane: "lane-1", Generation: 1, Size: 0, Limit: 2000})
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"lane", "scrollback", "lane-1"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "no scrollback yet") {
		t.Errorf("stdout %q, stderr %q", stdout.String(), stderr.String())
	}
}

// A count the API would refuse is refused before the call.
func TestLaneScrollbackRefusesACountOutOfBounds(t *testing.T) {
	for _, count := range []string{"0", "-1", "10001"} {
		p := newPlane(t)
		scrolled(p, api.PaneScrollback{})
		var stdout, stderr bytes.Buffer
		code := cli.Run([]string{"lane", "scrollback", "lane-1", "--lines", count}, &stdout, &stderr, cli.Options{Socket: p.Socket})
		if code != cli.ExitUsage || !strings.Contains(stderr.String(), "from 1 to 10000") {
			t.Errorf("--lines %s: exit %d, stderr %q", count, code, stderr.String())
		}
		for _, read := range p.reads() {
			if strings.Contains(read, "scrollback") {
				t.Errorf("--lines %s still made the call %s", count, read)
			}
		}
	}
}

func TestLaneScrollbackTakesOneLane(t *testing.T) {
	for _, args := range [][]string{{"lane", "scrollback"}, {"lane", "scrollback", "lane-1", "lane-2"}} {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(args, &stdout, &stderr, cli.Options{}); code != cli.ExitUsage || !strings.Contains(stderr.String(), "takes one lane ID") {
			t.Errorf("%v: exit %d, stderr %q", args, code, stderr.String())
		}
	}
}

// A lane with no pane to read, such as a parked one, is the API's refusal: the command prints nothing, says why on
// stderr, exits 1 as for a refusal with no code of its own, and with --json names the code.
func TestLaneScrollbackSaysWhyALaneHasNoPane(t *testing.T) {
	p := newPlane(t)
	p.on(scrollbackRoute, func(*http.Request, []byte) (any, *api.Error) {
		return nil, &api.Error{Status: http.StatusConflict, Detail: api.ErrorDetail{Code: api.CodeNoPane, Message: "lane lane-1 is parked, so it has no pane"}}
	})
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"lane", "scrollback", "lane-1"}, &stdout, &stderr, cli.Options{Socket: p.Socket})
	if code != cli.ExitError || stdout.Len() != 0 || !strings.Contains(stderr.String(), "lane lane-1 is parked, so it has no pane") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	cli.Run([]string{"lane", "scrollback", "lane-1", "--json"}, &stdout, &stderr, cli.Options{Socket: p.Socket})
	var body api.ErrorBody
	if err := json.Unmarshal(stderr.Bytes(), &body); err != nil || body.Error.Code != api.CodeNoPane {
		t.Errorf("--json said %q on stderr: %v", stderr.String(), err)
	}
}

// --json prints the API's own answer, with the lines as the pane holds them and the counts around them.
func TestLaneScrollbackReadsAsJSON(t *testing.T) {
	p := newPlane(t)
	scrolled(p, api.PaneScrollback{Lane: "lane-1", Generation: 2, Lines: "\x1b[1mone\x1b[0m\n", Size: 9, Limit: 2000})
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"lane", "scrollback", "lane-1", "--json"}, &stdout, &stderr, cli.Options{Socket: p.Socket}); code != cli.ExitOK {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var got api.PaneScrollback
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.Lines != "\x1b[1mone\x1b[0m\n" || got.Size != 9 || got.Limit != 2000 || got.Generation != 2 {
		t.Errorf("printed %q: %v", stdout.String(), err)
	}
	if stderr.Len() != 0 {
		t.Errorf("--json said %q on stderr", stderr.String())
	}
}
