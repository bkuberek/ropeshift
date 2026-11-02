package cli_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"ropeshift.dev/ropeshift/internal/api"
	"ropeshift.dev/ropeshift/internal/cli"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/test/apitest"
)

// TestEveryWebRouteHasACommand is the plan's rule that the UI has no endpoint the CLI lacks. The web
// listener serves a closed list, and each row of it names a command, which must exist.
func TestEveryWebRouteHasACommand(t *testing.T) {
	for _, route := range api.WebRoutes() {
		if !cli.HasCommand(route.Command) {
			t.Errorf("%s %s names %q, and no such command exists", route.Method, route.Pattern, route.Command)
		}
	}
}

func TestEveryViewReadsFromTheCommandLine(t *testing.T) {
	s := newSession(t)
	for _, name := range api.Views {
		t.Run(name, func(t *testing.T) {
			var stamp api.Stamp
			if err := json.Unmarshal([]byte(s.must("view", name, "--json")), &stamp); err != nil {
				t.Fatal(err)
			}
			if stamp.AsOf == 0 {
				t.Errorf("stamp %+v", stamp)
			}
		})
	}
}

func TestAViewReadsAsTextToo(t *testing.T) {
	s := newSession(t)
	if text := s.must("view", api.ViewNeedsYou); !strings.Contains(text, "items") {
		t.Errorf("view %q", text)
	}
}

func TestAViewFiltersByCategory(t *testing.T) {
	s := newSession(t)
	s.must("question", "record", "--text", "May it stay?", "--category", "product")
	var view api.Questions
	if err := json.Unmarshal([]byte(s.must("view", api.ViewQuestions, "--category", "no-such-category", "--json")), &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Questions) != 0 {
		t.Errorf("questions %+v", view.Questions)
	}
}

func TestAViewNobodyServesExitsAsUsage(t *testing.T) {
	s := newSession(t)
	if code, _, _ := s.run("view", "no-such-view"); code != cli.ExitUsage {
		t.Errorf("exit %d", code)
	}
}

func TestEffectCommands(t *testing.T) {
	s := newSession(t)
	w := apitest.NewWorld(t, s.daemon)
	lane := w.Running("wo-effect", "Add the retry budget")
	raised := lane.Effect("lane-wo-effect/1/input/nudge", "input", 1)

	if text := s.must("effect", "list"); !strings.Contains(text, raised.Key) || !strings.Contains(text, "waiting") {
		t.Errorf("list %q", text)
	}
	var row ledger.UnknownEffect
	if err := json.Unmarshal([]byte(s.must("effect", "show", raised.ID, "--json")), &row); err != nil {
		t.Fatal(err)
	}
	if row.Key != raised.Key {
		t.Errorf("row %+v", row)
	}
	if code, _, _ := s.run("effect", "decide", raised.ID); code != cli.ExitUsage {
		t.Errorf("no outcome: exit %d", code)
	}
	if code, _, _ := s.run("effect", "decide", raised.ID, "--outcome", "maybe"); code != cli.ExitUsage {
		t.Errorf("no such outcome: exit %d", code)
	}
	if code, _, _ := s.run("effect", "decide", "no-such-effect", "--outcome", "happened"); code != cli.ExitNotFound {
		t.Errorf("no such effect: exit %d", code)
	}
	s.must("effect", "decide", raised.ID, "--outcome", "retry", "--reason", "It never arrived")
	if text := s.must("effect", "show", raised.ID); !strings.Contains(text, "retry") {
		t.Errorf("show %q", text)
	}
	if code, _, _ := s.run("effect", "decide", raised.ID, "--outcome", "happened"); code != cli.ExitConflict {
		t.Errorf("a second word: exit %d", code)
	}
}

func TestEventsFollowTheLedgerFromARevision(t *testing.T) {
	s := newSession(t)
	s.create("wo")
	text := s.must("events", "--after", "0", "--limit", "2", "--json")
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines: %q", len(lines), text)
	}
	var revisions []int64
	for _, line := range lines {
		var event api.StreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		revisions = append(revisions, event.Rev)
	}
	if !slices.Equal(revisions, []int64{1, 2}) {
		t.Errorf("revisions %v", revisions)
	}
}

func TestSignOutEndsTheSession(t *testing.T) {
	s := newSession(t)
	s.must("sign-out")
	if code, _, stderr := s.run("work", "create", "--title", "After the session"); code != cli.ExitForbidden {
		t.Errorf("exit %d: %s", code, stderr)
	}
}
