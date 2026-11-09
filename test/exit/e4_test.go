package exit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"ropeshift.com/ropeshift/internal/api"
	"ropeshift.com/ropeshift/internal/api/shimlink"
	"ropeshift.com/ropeshift/internal/ledger"
	"ropeshift.com/ropeshift/internal/messages"
	"ropeshift.com/ropeshift/internal/workorders"
	"ropeshift.com/ropeshift/test/contract"
	"ropeshift.com/ropeshift/test/doubles"
	"ropeshift.com/ropeshift/test/gittest"
)

func (in *install) laneView(lane string) (ledger.LaneView, bool) {
	in.t.Helper()
	code, out, _ := in.run("lane", "show", lane, "--json")
	var view ledger.LaneView
	return view, code == 0 && json.Unmarshal([]byte(out), &view) == nil
}

func (in *install) laneReads(lane, state string) func() bool {
	return func() bool {
		view, ok := in.laneView(lane)
		return ok && view.ActualState == state
	}
}

// workOrders waits for intake to read this many, and names them by what intake gave them.
func (in *install) workOrders(count int) map[string]string {
	in.t.Helper()
	ids := map[string]string{}
	in.waitFor("intake", func() bool {
		var list api.WorkOrderList
		if err := json.Unmarshal([]byte(in.must("work", "list", "--json")), &list); err != nil {
			in.t.Fatal(err)
		}
		for _, row := range list.WorkOrders {
			ids[row.Title] = row.ID
		}
		return len(list.WorkOrders) == count
	})
	return ids
}

// theQuestion waits for the one question lanes have asked.
func (in *install) theQuestion() string {
	in.t.Helper()
	var id string
	in.waitFor("the question", func() bool {
		var list api.QuestionList
		if err := json.Unmarshal([]byte(in.must("question", "list", "--json")), &list); err != nil {
			in.t.Fatal(err)
		}
		if len(list.Questions) != 1 {
			return false
		}
		id = list.Questions[0].ID
		return true
	})
	return id
}

func (in *install) messagesTo(lane string) []messages.Message {
	in.t.Helper()
	var list []messages.Message
	if err := json.Unmarshal([]byte(in.must("message", "list", lane, "--json")), &list); err != nil {
		in.t.Fatal(err)
	}
	return list
}

// firstPrompts reads what the scripted CLI took on stdin, one line a prompt.
func (in *install) firstPrompts(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(doubles.BinDir(in.path), "prompts.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// lingering writes a lane's own profile, which holds it at rest past the end of a test.
func lingering(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rest.toml")
	if err := os.WriteFile(path, []byte(restProfile), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (in *install) startWorker(repo string) *daemon {
	in.t.Helper()
	running := in.start(parkFlags(repo)...)
	in.waitReady(running)
	in.waitFor("the worker's session", func() bool {
		list := in.workers()
		return len(list) == 1 && list[0].Epoch >= 1
	})
	return running
}

func (in *install) laneOn(t *testing.T, workOrder, claim, lane string, flags ...string) {
	t.Helper()
	in.must("work", "review", workOrder)
	in.must("work", "claim", workOrder, "--worker", in.workers()[0].ID, "--claim-id", claim)
	in.must(append([]string{"lane", "start", workOrder, "--lane", lane}, flags...)...)
	doubles.ReapShims(t, filepath.Join(in.paths().StateDir, "lanes", lane))
}

// checkResumedWithIt reads what a lane went on from: a new generation, and an answer it was given.
func (in *install) checkResumedWithIt(t *testing.T, lane, workOrder, answer string) {
	t.Helper()
	in.waitFor("the lane's outcome", func() bool { return in.show(workOrder).State == workorders.Acceptance })
	view, ok := in.laneView(lane)
	if !ok {
		t.Fatalf("no view of %s", lane)
	}
	if len(view.Generations) != 2 || view.Generations[1].StartReason != ledger.StartResume {
		t.Errorf("generations %+v", view.Generations)
	}
	if prompts := in.firstPrompts(t); !strings.Contains(prompts, answer) {
		t.Errorf("no prompt held the answer:\n%s", prompts)
	}
	resumed := slices.ContainsFunc(in.sessionsOf(t), func(args []string) bool { return slices.Contains(args, "--resume") })
	if !resumed {
		t.Errorf("no session resumed: %v", in.sessionsOf(t))
	}
}

// TestExitE4ParkAndResume is the question half of E4 on the real binary, the real driver, and the
// real shim. Two lanes wait on one question: one parks with nothing of it running, and one stays at
// rest. One answer reaches both, as a next generation's prompt and as a message.
func TestExitE4ParkAndResume(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", parkProfile)
	repo.Commit("alpha.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	repo.Commit("beta.md", contract.ContractText("beta", "Beta", "Add the backoff."))
	in.path = doubles.InstallFakeClaude(t, "lanes-attach")
	running := in.startWorker(repo.Dir)
	orders := in.workOrders(2)
	first, second := orders["The work order of alpha"], orders["The work order of beta"]

	in.laneOn(t, first, "c1", "lane-1")
	in.waitFor("the lane to park", func() bool { return in.show(first).State == workorders.Parked })
	state, err := shimlink.ReadState(filepath.Join(in.paths().StateDir, "lanes", "lane-1", "shim-1"))
	if err != nil {
		t.Fatal(err)
	}
	in.waitFor("the session's end", func() bool { return !exists(state.ChildPID) && !exists(state.ShimPID) })

	in.laneOn(t, second, "c2", "lane-2", "--profile", lingering(t))
	in.waitFor("the second lane at rest", in.laneReads("lane-2", "idle"))
	question := in.theQuestion()

	in.must("question", "answer", question, "--answer", "Twice.", "--id", "d-1")

	in.checkResumedWithIt(t, "lane-1", first, "Twice.")
	in.waitFor("the second lane's outcome", func() bool { return in.show(second).State == workorders.Acceptance })
	got := in.messagesTo("lane-2")
	if len(got) != 1 || got[0].Kind != messages.KindAnswer || got[0].ReplyTo != question || !strings.Contains(got[0].Body, "Twice.") {
		t.Fatalf("what reached the lane at rest: %+v", got)
	}
	if got[0].State != messages.Admitted {
		t.Errorf("a lane that went on from an answer: %+v", got[0])
	}
	if view, ok := in.laneView("lane-2"); !ok || len(view.Generations) != 1 {
		t.Errorf("a lane that took the answer at rest: %+v", view.Generations)
	}
	running.kill()
}

// TestAnAnswerWhileALaneParksReachesItsNextGeneration lands in the window a lane is writing its
// narrative: the session is there and must not be written to, and the wake is not stored yet.
func TestAnAnswerWhileALaneParksReachesItsNextGeneration(t *testing.T) {
	in := newInstall(t)
	repo := gittest.New(t)
	repo.Commit(".ropeshift/profile.toml", parkProfile)
	repo.Commit("contract.md", contract.ContractText("alpha", "Alpha", "Add the retry budget."))
	in.path = doubles.InstallFakeClaude(t, "lane-parks-held")
	running := in.startWorker(repo.Dir)
	id := in.onlyWorkOrder()
	in.laneOn(t, id, "c1", "lane-1")
	in.waitFor("the lane parking", in.laneReads("lane-1", "parking"))

	in.must("question", "answer", in.theQuestion(), "--answer", "Twice.", "--id", "d-1")
	if err := os.WriteFile(filepath.Join(doubles.BinDir(in.path), "goes-on"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	in.checkResumedWithIt(t, "lane-1", id, "Twice.")
	got := in.messagesTo("lane-1")
	if len(got) != 1 || got[0].State != messages.Expired || got[0].Reason != messages.ReasonLedgerFact || got[0].CarriedIn != 2 {
		t.Errorf("an answer that landed in parking: %+v", got)
	}
	running.kill()
}
