package workorders

import (
	"bufio"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestTableMatchesTheDesign compares the table with a separate copy of the
// diagram, so a changed edge has to change both.
func TestTableMatchesTheDesign(t *testing.T) {
	file, err := os.Open("testdata/edges.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var want []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" && !strings.HasPrefix(line, "#") {
			want = append(want, line)
		}
	}
	var got []string
	for _, e := range Transitions() {
		if e.Trigger != TriggerCanceled {
			got = append(got, fmt.Sprintf("%s %s %s", e.From, e.Trigger, e.To))
		}
	}
	slices.Sort(want)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("table differs from the diagram\n got %v\nwant %v", got, want)
	}
}

func TestEveryNonTerminalStateCanCancel(t *testing.T) {
	for _, s := range States() {
		edge, ok := Next(s, TriggerCanceled)
		if Terminal(s) {
			if ok {
				t.Errorf("terminal state %s can cancel", s)
			}
			continue
		}
		if !ok || edge.To != Canceled {
			t.Errorf("%s cannot cancel: %+v", s, edge)
		}
	}
}

func TestTerminalStatesHaveNoExit(t *testing.T) {
	for _, e := range Transitions() {
		if Terminal(e.From) {
			t.Errorf("edge leaves terminal state: %+v", e)
		}
	}
}

func TestOneEdgePerStateAndTrigger(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range edges {
		key := string(e.From) + "/" + string(e.Trigger)
		if seen[key] {
			t.Errorf("two edges for %s", key)
		}
		seen[key] = true
	}
}

func TestEveryStateIsReachableAndCanFinish(t *testing.T) {
	reached := map[State]bool{Intake: true}
	for changed := true; changed; {
		changed = false
		for _, e := range Transitions() {
			if reached[e.From] && !reached[e.To] {
				reached[e.To], changed = true, true
			}
		}
	}
	finishes := map[State]bool{Done: true, Failed: true, Canceled: true}
	for changed := true; changed; {
		changed = false
		for _, e := range Transitions() {
			if finishes[e.To] && !finishes[e.From] {
				finishes[e.From], changed = true, true
			}
		}
	}
	for _, s := range States() {
		if !reached[s] {
			t.Errorf("%s is unreachable from intake", s)
		}
		if !finishes[s] {
			t.Errorf("%s can never finish", s)
		}
	}
}

func allTriggers() []Trigger {
	var out []Trigger
	for _, e := range Transitions() {
		if !slices.Contains(out, e.Trigger) {
			out = append(out, e.Trigger)
		}
	}
	return out
}

func legalTriggers() map[State][]Trigger {
	out := map[State][]Trigger{}
	for _, e := range Transitions() {
		out[e.From] = append(out[e.From], e.Trigger)
	}
	return out
}

// TestGeneratedSequences walks random triggers through the table. A trigger the
// table does not hold must change nothing, and a walk must never leave a terminal
// state. The seed is fixed, so a failure replays.
func TestGeneratedSequences(t *testing.T) {
	triggers := allTriggers()
	legal := legalTriggers()
	const walks, steps = 20000, 80
	rng := rand.New(rand.NewPCG(20261031, 3))
	visited := map[State]bool{}
	taken := map[string]bool{}
	for range walks {
		state := Intake
		for range steps {
			trigger := triggers[rng.IntN(len(triggers))]
			if options := legal[state]; len(options) > 0 && rng.IntN(2) == 0 {
				trigger = options[rng.IntN(len(options))]
			}
			edge, ok := Next(state, trigger)
			if !ok {
				continue
			}
			if Terminal(state) {
				t.Fatalf("%s left terminal state %s", trigger, state)
			}
			if edge.From != state || edge.Trigger != trigger {
				t.Fatalf("Next(%s, %s) returned %+v", state, trigger, edge)
			}
			taken[string(edge.From)+"/"+string(edge.Trigger)] = true
			state = edge.To
			visited[state] = true
		}
	}
	for _, s := range States() {
		if s != Intake && !visited[s] {
			t.Errorf("no walk reached %s", s)
		}
	}
	if want := len(Transitions()); len(taken) != want {
		t.Errorf("walks took %d of %d edges", len(taken), want)
	}
}

func TestOwnershipEdgesAreLeased(t *testing.T) {
	for _, e := range Transitions() {
		touchesHolder := holdsLease(e.From) || holdsLease(e.To)
		if touchesHolder && !e.Leased {
			t.Errorf("%s from %s changes a holder with no lease path", e.Trigger, e.From)
		}
	}
}
