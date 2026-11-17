package exit

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/adapters/sqlite"
	"ropeshift.com/ropeshift/internal/adapters/sqlstore"
	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/cli"
	"ropeshift.com/ropeshift/internal/control"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/ports"
	"ropeshift.com/ropeshift/internal/questions"
)

const soloScope = ledger.WorkspaceID("default")

// standInWindowMs is the window of the batch a stand-in for an orchestrator opens, in a test that does not
// depend on what the batch holds. The stand-in opens the batch, so its window applies until escalation has a
// route to the daemon. A test that does depend on it keeps the batch open with openBatchWindowMs and closes it
// with closeBatch.
const standInWindowMs = 400

// ledgerOf opens the running daemon's ledger, which is how a test stands in for an
// orchestrator until one exists.
func (in *install) ledgerOf() *sqlstore.Store {
	in.t.Helper()
	store, err := sqlite.Open(filepath.Join(in.paths().StateDir, api.LedgerFileName), sqlstore.Options{})
	if err != nil {
		in.t.Fatal(err)
	}
	in.t.Cleanup(func() { _ = store.Close() })
	return store
}

// openBatchWindowMs keeps the batch the stand-in opens open until a test closes it with closeBatch, so the
// batch holds every question the test escalates however long the escalations take.
const openBatchWindowMs = 600_000

// closeBatch brings the close of the open batch to now, once a test has escalated every question it means the
// batch to hold. The daemon's next tick delivers it. A batch that closes on a condition, and not on a window,
// does not lose a question to a slow machine.
func closeBatch(t *testing.T, ctx context.Context, store ports.Store) {
	t.Helper()
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}
	err := store.Write(ctx, soloScope, "test.close_batch", func(tx ports.WriteTx) error {
		batch, err := tx.Questions().OpenBatch()
		if err != nil {
			return err
		}
		moved, err := tx.Questions().HastenBatch(batch.ID, tx.Now())
		if err != nil || !moved {
			return err
		}
		batch.ClosesAt = tx.Now()
		event, err := questions.RecordEvent(questions.EventBatchChanged, questions.SubjectBatch, batch.ID, batch, orchestrator)
		if err != nil {
			return err
		}
		_, err = tx.Append(event)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// escalateWithin is escalate with the batch window the stand-in opens, for a test whose
// escalations are spread over more than the default window on a loaded machine.
func escalateWithin(t *testing.T, ctx context.Context, store ports.Store, id, category string, windowMs int64) {
	t.Helper()
	orchestrator := ledger.Actor{Kind: ledger.ActorOrchestrator, ID: "orchestrator-1"}
	use := control.NewQuestions(store, questions.BatchPolicy{WindowMs: windowMs, MaxWaitMs: 60_000})
	if _, err := use.Take(ctx, soloScope, id, 60_000, orchestrator); err != nil {
		t.Fatal(err)
	}
	for _, rung := range []int64{questions.RungLookup, questions.RungOpen} {
		step := questions.Step{Rung: rung, Candidates: []string{"decision-" + id}, Outcome: "no match"}
		if _, err := use.Climb(ctx, soloScope, id, step, orchestrator); err != nil {
			t.Fatal(err)
		}
	}
	_, err := use.Escalate(ctx, soloScope, id, questions.Escalation{
		Reason: questions.ReasonScopeChange, Options: []string{"Keep", "Change"}, Recommendation: "Keep",
		Categories: []string{category},
	}, orchestrator)
	if err != nil {
		t.Fatal(err)
	}
}

func (in *install) batches(flags ...string) api.BatchList {
	in.t.Helper()
	var list api.BatchList
	raw := in.must(append([]string{"question", "batches", "--json"}, flags...)...)
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		in.t.Fatalf("%v: %s", err, raw)
	}
	return list
}

// TestQuestionsReachMembersAsOneBatch takes five questions through the running daemon: its
// tick delivers, a member filters and answers once, and the ledger holds one interruption.
func TestQuestionsReachMembersAsOneBatch(t *testing.T) {
	in := newInstall(t)
	d := in.start("--batch-tick", "20ms")
	in.waitReady(d)
	store := in.ledgerOf()
	ctx := t.Context()
	categories := []string{"product", "product", "product", "product", "security"}
	var ids []string
	for i, category := range categories {
		id := fmt.Sprintf("q%d", i)
		in.must("question", "record", "--id", id, "--text", fmt.Sprintf("Question number %d?", i), "--category", category)
		ids = append(ids, id)
	}
	if got := in.batches().Batches; len(got) != 0 {
		t.Fatalf("delivered before any escalation: %+v", got)
	}
	for i, id := range ids {
		escalateWithin(t, ctx, store, id, categories[i], openBatchWindowMs)
	}
	if got := in.batches().Batches; len(got) != 0 {
		t.Fatalf("delivered before the batch closed: %+v", got)
	}
	closeBatch(t, ctx, store)
	in.waitFor("the batch", func() bool { return len(in.batches().Batches) == 1 })
	batch := in.batches().Batches[0]
	if len(batch.Items) != 5 {
		t.Fatalf("items %+v", batch.Items)
	}
	item := batch.Items[0]
	if len(item.Options) != 2 || item.Recommendation != "Keep" || len(item.Citations) != 1 {
		t.Fatalf("an item a member cannot answer once: %+v", item)
	}
	only := in.batches("--category", "security").Batches
	if len(only) != 1 || len(only[0].Items) != 1 || only[0].Items[0].ID != "q4" {
		t.Fatalf("the security filter: %+v", only)
	}
	if code, _, stderr := in.runWith(nil, "", nil, "question", "answer", "q4", "--answer", "Keep"); code != cli.ExitForbidden {
		t.Fatalf("an answer with no session: exit %d, %s", code, stderr)
	}
	in.must("question", "answer", "q4", "--answer", "Keep", "--rationale", "It follows.", "--id", "d4")
	if code, _, stderr := in.run("question", "answer", "q4", "--answer", "Change"); code != cli.ExitConflict {
		t.Fatalf("a second answer: exit %d, %s", code, stderr)
	}
	if got := in.batches("--category", "security").Batches; len(got) != 0 {
		t.Fatalf("an answered question stays: %+v", got)
	}
	if got := in.batches().Batches; len(got) != 1 || len(got[0].Items) != 4 {
		t.Fatalf("what still waits: %+v", got)
	}
	delivered := 0
	err := store.Read(ctx, soloScope, func(tx ports.ReadTx) error {
		events, err := tx.Events(0, 1000)
		for _, event := range events {
			if event.Type == questions.EventBatchDelivered {
				delivered++
			}
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if delivered != 1 {
		t.Fatalf("%d interruptions", delivered)
	}
}

// TestAMemberAnswersWithNoOrchestratorRunning holds D25: this daemon runs no orchestrator,
// so nothing works the ladder, and a member's answer still closes the question.
func TestAMemberAnswersWithNoOrchestratorRunning(t *testing.T) {
	in := newInstall(t)
	d := in.start()
	in.waitReady(d)
	in.must("question", "record", "--id", "q1", "--text", "What is the retry budget?")
	if text := in.must("question", "answer", "q1", "--answer", "Two retries."); !strings.Contains(text, "Two retries.") {
		t.Fatalf("the answer printed %q", text)
	}
	if text := in.must("question", "show", "q1"); !strings.Contains(text, string(questions.StatusAnswered)) {
		t.Fatalf("the question reads %q", text)
	}
}
