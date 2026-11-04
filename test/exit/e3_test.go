package exit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ropeshift.dev/ropeshift/internal/adapters/profiles"
	"ropeshift.dev/ropeshift/internal/control"
	"ropeshift.dev/ropeshift/internal/ledger"
	"ropeshift.dev/ropeshift/internal/ports"
	"ropeshift.dev/ropeshift/internal/questions"
	"ropeshift.dev/ropeshift/test/doubles"
)

// wakesNow keeps an orchestrator's wakes from waiting on a person's scale.
const wakesNow = "[lifecycle]\nwake_debounce = \"10ms\"\nwake_min_interval = \"20ms\"\nask_wait = \"5m\"\nhandoff_timeout = \"2s\"\n"

// TestExitE3LadderAndAttach runs a scripted orchestrator through the real daemon, the real driver,
// and the real `ropeshift mcp`: a rung 1 answer, a rung 2 attach, a rung 3 decision, a rung 4
// assumption, and rung 5 through the gate. A member starts it, it wakes once with the whole batch,
// and nothing happens before that.
func TestExitE3LadderAndAttach(t *testing.T) {
	in := newInstall(t)
	in.path = doubles.InstallFakeClaude(t, "orchestrator-ladder")
	if err := os.MkdirAll(in.paths().StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in.paths().StateDir, profiles.UserFile), []byte(wakesNow), 0o600); err != nil {
		t.Fatal(err)
	}
	d := in.start("--heartbeat", "30ms", "--lease-tick", "20ms")
	defer func() {
		d.kill()
		if t.Failed() {
			t.Logf("the daemon said: %s", d.stderr)
		}
	}()
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	worker := in.workers()[0].ID

	store := in.ledgerOf()
	use := control.NewQuestions(store, questions.BatchPolicy{WindowMs: standInWindowMs, MaxWaitMs: 60_000})
	member := ledger.Actor{Kind: ledger.ActorMember, ID: "member-1"}
	ctx := t.Context()
	_, err := use.RecordDecision(ctx, soloScope, questions.Decision{
		ID: "d-budget", Status: questions.DecisionDecided, Statement: "Retry twice.", Rationale: "It follows.", Scope: "retries",
		Reversibility: "reversible", DecidedBy: member.ID, Citations: []string{"PAY-12"},
	}, member)
	if err != nil {
		t.Fatal(err)
	}
	asks := []questions.Ask{
		{ID: "rung-1", Text: "How many retries?"},
		{ID: "rung-2", Text: "Which retry budget applies?"},
		{ID: "rung-2-again", Text: "What is the retry budget, again?"},
		{ID: "rung-3", Text: "What level do retries log at?"},
		{ID: "rung-4", Text: "Which queue carries retries?"},
		{ID: "rung-5", Text: "Do we rotate the signing key?", Categories: []string{questions.CategorySecurity}},
	}
	for _, ask := range asks {
		if _, err := use.Ask(ctx, soloScope, ask, member); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(doubles.BinDir(in.path), "seen")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a session no member asked for: %v", in.sessionsOf(t))
	}

	in.must("orchestrator", "start", worker)
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", ledger.Orchestrator{ID: ledger.OrchestratorOf(worker), Run: 1}.Lane()))

	read := func(id string) questions.Question {
		var out questions.Question
		err := store.Read(ctx, soloScope, func(tx ports.ReadTx) error {
			var err error
			out, err = tx.Questions().Question(id)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	in.waitFor("the ladder's last rung", func() bool { return read("rung-5").Status == questions.StatusNeedsHuman })

	if got := read("rung-1"); got.Status != questions.StatusAnswered || got.DecisionID != "d-budget" {
		t.Errorf("rung 1: %+v", got)
	}
	if got := read("rung-2-again"); got.Status != questions.StatusDismissed {
		t.Errorf("rung 2: %+v", got)
	} else if into, merged := got.MergedInto(); !merged || into != "rung-2" {
		t.Errorf("rung 2 merged into %q", into)
	}
	if got := read("rung-3"); got.Status != questions.StatusAnswered {
		t.Errorf("rung 3: %+v", got)
	}
	rung4 := read("rung-4")
	if rung4.Status != questions.StatusAnswered {
		t.Fatalf("rung 4: %+v", rung4)
	}
	err = store.Read(ctx, soloScope, func(tx ports.ReadTx) error {
		decision, err := tx.Questions().Decision(rung4.DecisionID)
		if err != nil {
			return err
		}
		if decision.Status != questions.DecisionAssumed {
			t.Errorf("rung 4 reads %s", decision.Status)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read("rung-5"); !got.Ran(questions.RungLookup) || !got.Ran(questions.RungOpen) {
		t.Errorf("rung 5 passed the gate with no trace: %+v", got.LadderTrace)
	}
	if sessions := in.sessionsOf(t); len(sessions) != 1 {
		t.Errorf("%d sessions for one wake: %v", len(sessions), sessions)
	}
}

// A second orchestrator start names the same one, and no agent could have made the first.
func TestOnlyAMembersSessionStartsAnOrchestrator(t *testing.T) {
	in := newInstall(t)
	d := in.start("--heartbeat", "30ms")
	defer d.kill()
	in.waitReady(d)
	in.waitFor("the worker's session", func() bool { return len(in.workers()) == 1 })
	worker := in.workers()[0].ID
	session := in.session
	in.session = ""
	if code, _, _ := in.run("orchestrator", "start", worker); code == 0 {
		t.Error("a start with no session")
	}
	in.session = session
	if code, _, _ := in.run("orchestrator", "start", "no-such-worker"); code == 0 {
		t.Error("a start on a worker nobody registered")
	}
	in.must("orchestrator", "start", worker)
	in.must("orchestrator", "show", worker)
	in.must("orchestrator", "stop", worker)
}
